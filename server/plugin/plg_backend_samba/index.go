package plg_backend_samba

import (
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hirochachacha/go-smb2"
	. "github.com/mickael-kerjean/filestash/server/common"
)

var SambaCache AppCache

func init() {
	Backend.Register("samba", &Samba{})

	SambaCache = NewAppCache(60*time.Minute, 10*time.Minute) // HTL: long retention - the janitor must never tear down a live session (see e010ac7d).
	SambaCache.OnEvict(func(key string, value interface{}) {
		smb := value.(*Samba)
		if smb.inflight.Load() != 0 {
			SambaCache.SetKey(key, smb) // downloads mid-flight: re-arm the eviction instead of killing (and leaking) a busy session
			return
		}
		smb.Close()
	})
}

// HTL (fork): the pinned go-smb2 fork (v1.1.0 based) has NO keepalive and NO
// reconnect - see hirochachacha/go-smb2#68. Whenever Windows/firewall drops
// the TCP connection (typical around long-idle periods; the download itself
// is just what keeps the socket warm), the library latches the net error on
// its connection and EVERY later operation fails with
//
//	"connection error: read tcp ...: connection reset by peer"
//
// until a brand new session is established - hence the old "only re-login
// helps" symptom. Fix used here (same idea as upstream's unreleased `client`
// package): remember the dial parameters, detect the poisoned transport (or
// an explicit "session gone" nt status), transparently re-dial with the same
// credentials - a fresh SMB2 session setup is fully independent of the old
// one - and re-run the failed operation exactly once.
type Samba struct {
	// dial parameters, immutable after Init so a reconnect can reuse them
	server   string // host without port
	port     string
	username string
	password string
	domain   string
	wants    string // requested share name; "" = discover everything except admin $

	mu       sync.Mutex // guards the session state below AND the reconnect itself
	session  *smb2.Session
	share    map[string]*smb2.Share
	fd       net.Conn // underlying TCP socket; closed by hand on drop because the library only closes it after a SUCCESSFUL logoff (after a RST, Logoff returns the latched error and the fd would leak)
	inflight *atomic.Int32
}

func (smb *Samba) Init(params map[string]string, app *App) (IBackend, error) {
	if strings.HasPrefix(params["host"], "smb://") == false {
		params["host"] = "smb://" + params["host"]
	}
	if u, err := url.Parse(params["host"]); err == nil {
		params["host"] = u.Host
		if params["port"] == "" {
			params["port"] = u.Port()
		}
		if params["share"] == "" {
			params["share"] = strings.ReplaceAll(u.Path, "/", "")
		}
		if params["username"] == "" && u.User != nil {
			params["username"] = u.User.Username()
		}
		if params["password"] == "" && u.User != nil {
			params["password"], _ = u.User.Password()
		}
	}
	if params["port"] == "" {
		params["port"] = "445"
	}
	// remember everything a transparent reconnect needs later on
	smb.username = strings.TrimSpace(params["username"])
	smb.password = params["password"]
	smb.domain = params["domain"]
	smb.port = params["port"]
	smb.server = params["host"]
	smb.share = make(map[string]*smb2.Share, 0)
	smb.inflight = &atomic.Int32{}
	smb.wants = params["share"]
	if c := SambaCache.Get(params); c != nil {
		return c.(*Samba), nil
	}
	if err := smb.dial(); err != nil {
		return nil, err
	}
	SambaCache.Set(params, smb)
	return smb, nil
}

// dial establishes the TCP connection + SMB session and mounts the shares.
// It is used both at Init time and whenever the previous connection turned
// out to be dead. Callers either own the fresh struct (Init) or hold smb.mu
// (reconnect path).
func (smb *Samba) dial() error {
	host := net.JoinHostPort(smb.server, smb.port)
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		Log.Debug("plg_backend_samba::netdial host[%s] err[%s]", host, err.Error())
		return err
	}
	session, err := (&smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User: func() string {
				if smb.username == "" {
					return "Guest"
				}
				return smb.username
			}(),
			Password: smb.password,
			Domain:   smb.domain,
		},
	}).Dial(conn)
	if err != nil {
		conn.Close() // nothing references this socket yet, close it by hand
		Log.Debug("plg_backend_samba::smbdial host[%s] err[%s] username[%s] domain[%s]", host, err.Error(), smb.username, smb.domain)
		return err
	}
	share := make(map[string]*smb2.Share, 0)
	if smb.wants == "" {
		names, err := session.ListSharenames()
		if err != nil {
			conn.Close()
			Log.Debug("plg_backend_samba::list host[%s] err[%s]", host, err.Error())
			return err
		}
		for _, name := range names {
			if strings.HasSuffix(name, "$") {
				continue
			}
			if m, err := session.Mount(name); err == nil {
				share[name] = m
			}
		}
	} else {
		if m, err := session.Mount(smb.wants); err == nil {
			share[smb.wants] = m
		}
	}
	smb.session = session
	smb.share = share
	smb.fd = conn
	return nil
}

// Close tears the whole session down (cache janitor path). A download that
// is mid-flight keeps the session in the cache instead (inflight > 0) - the
// janitor simply comes back later.
func (smb *Samba) Close() {
	smb.mu.Lock()
	defer smb.mu.Unlock()
	if smb.inflight.Load() != 0 {
		return
	}
	smb.releaseLocked()
}

// releaseLocked closes every fd + handle this backend owns. Callers hold
// smb.mu. Umount/Logoff on an already dead connection cannot do their round
// trip and only return the latched transport error - harmless. The TCP fd is
// ALWAYS closed by hand: the library closes it only on a successfull logoff,
// so skipping it would leak one socket per dropped session.
func (smb *Samba) releaseLocked() {
	if smb.fd != nil {
		smb.fd.Close()
		smb.fd = nil
	}
	for key := range smb.share {
		smb.share[key].Umount()
	}
	smb.share = make(map[string]*smb2.Share, 0)
	if smb.session != nil {
		smb.session.Logoff()
		smb.session = nil
	}
}

// isConnLost reports whether err proves that the underlying SMB connection
// is unusable for good:
//   - smb2.TransportError: the net error latched on our socket after the
//     first RST/EOF/timeout. From that point on, every subsequent call on the
//     same connection fails instantly with the same error without touching
//     the network, which is what makes the redial+retry below safe from
//     loops: the retry either succeeds on the new connection or the same
//     latching happens on it once.
//   - smb2.ResponseError with a server-side "your session/credentials are
//     no longer valid" nt status. The server itself declared the session
//     dead; a fresh session setup is the only way forward too.
func isConnLost(err error) bool {
	if err == nil {
		return false
	}
	var terr *smb2.TransportError
	if errors.As(err, &terr) {
		return true
	}
	var rerr *smb2.ResponseError
	if errors.As(err, &rerr) {
		switch rerr.Code {
		case 0xC0000203, // STATUS_USER_SESSION_DELETED
			0xC000035C, // STATUS_NETWORK_SESSION_EXPIRED
			0xC0000064, // STATUS_NO_SUCH_USER
			0xC000006A, // STATUS_WRONG_PASSWORD
			0x00000002: // STATUS_LOGON_FAILURE (server rotated credentials)
			return true
		}
	}
	return false
}

// reconnect replaces a dead session with a fresh one. Caller holds smb.mu.
func (smb *Samba) reconnect(why error) error {
	Log.Info("samba: connection lost (%s) - reconnecting to %s as %s", why.Error(), smb.server, smb.username)
	smb.releaseLocked()
	if err := smb.dial(); err != nil {
		Log.Warning("samba: reconnect to %s failed: %v", smb.server, err.Error())
		return err
	}
	return nil
}

// Home (HTL fork): every user's working folder lives under /Users/<username>
// on the fileserver (their "H:" drive). Reporting it as the session home
// makes the webapp land there right after login ("/" would be a wall of
// system shares nobody should browse). The value is validated against the
// REAL filesystem so a mismatched username (eg. someone typing the domain
// form "DOMAIN\user") simply falls back to the storage root.
func (smb *Samba) Home() (string, error) {
	if smb.username == "" || smb.username == "Guest" || strings.Contains(smb.username, "\\") {
		return "", ErrNotFound
	}
	home := "/Users/" + smb.username + "/"
	if _, err := smb.Stat(home); err != nil {
		return "", err
	}
	return home, nil
}

func (smb *Samba) LoginForm() Form {
	return Form{
		Elmnts: []FormElement{
			{
				Name:  "type",
				Type:  "hidden",
				Value: "samba",
			},
			{
				Name:        "host",
				Type:        "text",
				Placeholder: "Hostname",
			},
			{
				Name:        "username",
				Type:        "text",
				Placeholder: "Username",
			},
			{
				Name:        "password",
				Type:        "password",
				Placeholder: "Password",
			},
			{
				Name:        "advanced",
				Type:        "enable",
				Placeholder: "Advanced",
				Target:      []string{"samba_port", "samba_path", "samba_domain", "samba_share"},
			},
			{
				Id:          "samba_path",
				Name:        "path",
				Type:        "text",
				Placeholder: "Path",
			},
			{
				Id:          "samba_port",
				Name:        "port",
				Type:        "number",
				Placeholder: "Port - eg: 445",
			},
			{
				Id:          "samba_domain",
				Name:        "domain",
				Type:        "text",
				Placeholder: "Domain",
			},
			{
				Id:          "samba_share",
				Name:        "share",
				Type:        "text",
				Placeholder: "Share Name",
			},
		},
	}
}

func (smb *Samba) Ls(path string) ([]os.FileInfo, error) {
	smb.mu.Lock()
	defer smb.mu.Unlock()
	if path == "/" {
		f := make([]os.FileInfo, 0)
		for key := range smb.share {
			f = append(f, File{
				FName: key,
				FType: "directory",
			})
		}
		return f, nil
	}
	share, opath, err := smb.getShareLocked(path)
	if err != nil {
		return nil, err
	}
	dir, err := share.Open(opath)
	if err != nil {
		if isConnLost(err) && smb.reconnect(err) == nil {
			if share, opath, err = smb.getShareLocked(path); err != nil {
				return nil, err
			}
			if dir, err = share.Open(opath); err != nil {
				return nil, fromSambaErr(err)
			}
		} else {
			return nil, fromSambaErr(err)
		}
	}
	defer dir.Close()

	fs, err := dir.Readdir(-1)
	return fs, fromSambaErr(err)
}

func (smb *Samba) Stat(path string) (os.FileInfo, error) {
	smb.mu.Lock()
	defer smb.mu.Unlock()
	if path == "/" {
		for key := range smb.share {
			return File{
				FName: key,
				FType: "directory",
				FTime: -1,
				FSize: 0,
			}, nil
		}
		return nil, ErrNotFound
	}
	share, opath, err := smb.getShareLocked(path)
	if err != nil {
		return nil, err
	}
	f, err := share.Stat(opath)
	if err != nil && isConnLost(err) && smb.reconnect(err) == nil {
		if share, opath, err = smb.getShareLocked(path); err != nil {
			return nil, err
		}
		f, err = share.Stat(opath)
	}
	return f, fromSambaErr(err)
}

func (smb *Samba) Cat(path string) (io.ReadCloser, error) {
	smb.mu.Lock()
	defer smb.mu.Unlock()
	share, opath, err := smb.getShareLocked(path)
	if err != nil {
		return nil, err
	}
	f, err := share.Open(opath)
	if err != nil {
		if isConnLost(err) && smb.reconnect(err) == nil {
			if share, opath, err = smb.getShareLocked(path); err != nil {
				return nil, err
			}
			if f, err = share.Open(opath); err != nil {
				return nil, fromSambaErr(err)
			}
		} else {
			return nil, fromSambaErr(err)
		}
	}
	return NewReadahead(f, smb.inflight), nil
}

func (smb *Samba) Mkdir(path string) error {
	return smb.run(path, func(share *smb2.Share, opath string) error {
		return share.Mkdir(opath, os.ModeDir)
	})
}

func (smb *Samba) Rm(path string) error {
	return smb.run(path, func(share *smb2.Share, opath string) error {
		return share.RemoveAll(opath)
	})
}

func (smb *Samba) Mv(from, to string) error {
	fromSharename, fromPath, err := smb.splitPath(from)
	if err != nil {
		return err
	}
	toSharename, toPath, err := smb.splitPath(to)
	if err != nil {
		return err
	}
	if fromSharename != toSharename {
		return ErrNotImplemented
	}
	return smb.run(from, func(share *smb2.Share, _ string) error {
		return share.Rename(fromPath, toPath)
	})
}

func (smb *Samba) Save(path string, content io.Reader) error {
	smb.inflight.Add(1)
	defer smb.inflight.Add(-1)
	return smb.run(path, func(share *smb2.Share, opath string) error {
		f, err := share.Create(opath)
		if err != nil {
			return fromSambaErr(err)
		}
		if _, err = io.Copy(f, content); err != nil {
			f.Close()
			return fromSambaErr(err)
		}
		return fromSambaErr(f.Close())
	})
}

func (smb *Samba) Touch(path string) error {
	return smb.run(path, func(share *smb2.Share, opath string) error {
		f, err := share.Create(opath)
		if err != nil {
			return fromSambaErr(err)
		}
		return fromSambaErr(f.Close())
	})
}

// run resolves the path, executes fn against the share and - when fn fails
// with a fatal conn error - rebuilds the session exactly once and retries.
func (smb *Samba) run(path string, fn func(share *smb2.Share, opath string) error) error {
	smb.mu.Lock()
	defer smb.mu.Unlock()
	share, opath, err := smb.getShareLocked(path)
	if err != nil {
		return err
	}
	err = fn(share, opath)
	if err != nil && isConnLost(err) && smb.reconnect(err) == nil {
		if share, opath, err = smb.getShareLocked(path); err != nil {
			return err
		}
		err = fn(share, opath)
	}
	return fromSambaErr(err)
}

// getShareLocked resolves "/Share/dir/file" to (share object, "dir\\file").
// Caller holds smb.mu.
func (smb *Samba) getShareLocked(path string) (*smb2.Share, string, error) {
	sharename, opath, err := smb.splitPath(path)
	if err != nil {
		return nil, "", err
	}
	share := smb.share[sharename]
	if share == nil {
		return nil, "", ErrNotFound
	}
	return share, opath, nil
}

// splitPath parses "/Share/a/b" into the share name + smb-relative path. The
// caller decides what to do with the share name (Mv needs it to reject
// cross-share moves).
func (smb *Samba) splitPath(path string) (string, string, error) {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 0 || p[0] == "" {
		return "", "", ErrNotAllowed
	}
	return p[0], strings.TrimLeft(strings.Join(p[1:], "\\"), "\\"), nil
}

func fromSambaErr(err error) error {
	switch {
	case os.IsPermission(err):
		return ErrPermissionDenied
	case os.IsNotExist(err):
		return ErrNotFound
	default:
		return err
	}
}
