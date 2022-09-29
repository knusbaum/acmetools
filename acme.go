package acmetools

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/knusbaum/go9p/client"
	"github.com/knusbaum/go9p/proto"
)

// Namespace duplicates plan9port's getns behavior
func Namespace() (string, error) {
	ns := os.Getenv("NAMESPACE")
	if ns == "" {
		disp := os.Getenv("DISPLAY")
		if disp == "" {
			return "", fmt.Errorf("$NAMESPACE not set, $DISPLAY not set")
		}

		disp = canonicalize(disp)
		disp = strings.Replace(disp, "/", "_", -1)
		var uname string
		u, err := user.Current()
		if err != nil {
			uname = "none"
		} else {
			uname = u.Username
		}
		ns = fmt.Sprintf("/tmp/ns.%s.%s", uname, disp)

		err = os.MkdirAll(ns, 0700)
		if err != nil {
			return "", err
		}
		return ns, nil
	}
	return ns, nil
}

func canonicalize(disp string) string {
	po := []rune(disp)
	i := 0
	for ; i < len(po) && po[i] != ':'; i++ {
	}
	i++
	if i >= len(po) {
		return disp
	}
	for ; i < len(po) && unicode.IsDigit(po[i]); i++ {
	}
	if string(po[i:]) == ".0" {
		po = po[:i]
		return string(po)
	}
	return disp
}

type Acme struct {
	c *client.Client
}

type Window struct {
	c  *client.Client
	id string

	addr *client.File
	ctl  *client.File
	data *client.File
}

func NewAcme() (*Acme, error) {
	ns, err := Namespace()
	if err != nil {
		return nil, fmt.Errorf("Can't locate namespace: %w", err)
	}
	acmef, err := net.Dial("unix", path.Join(ns, "acme"))
	if err != nil {
		return nil, fmt.Errorf("Failed to dial acme: %w", err)
	}
	npc, err := client.NewClient(acmef, "kjn", "")
	if err != nil {
		return nil, fmt.Errorf("Failed to attach to acme: %w", err)
	}
	return &Acme{npc}, nil
}

func (a *Acme) GetWindow(id string) (*Window, error) {
	_, err := a.c.Stat("/" + id)
	if err != nil {
		return nil, err
	}
	return &Window{c: a.c, id: id}, nil
}

func (w *Window) Close() error {
	if w.addr != nil {
		w.addr.Close()
		w.addr = nil
	}
	if w.ctl != nil {
		w.ctl.Close()
		w.ctl = nil
	}
	if w.data != nil {
		w.data.Close()
		w.data = nil
	}
	return nil
}

func (w *Window) Ctl(msg string) error {
	if w.ctl == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/ctl", w.id), proto.Ordwr)
		if err != nil {
			return fmt.Errorf("Tried to open ctl file, but failed: %w", err)
		}
		w.ctl = f
	}
	_, err := fmt.Fprintf(w.ctl, "%s\n", msg)
	return err
}

func (w *Window) WriteAddr(a string) error {
	if w.addr == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/addr", w.id), proto.Ordwr)
		if err != nil {
			return fmt.Errorf("Tried to open addr file, but failed: %w", err)
		}
		w.addr = f
	}
	_, err := io.WriteString(w.addr, a)
	return err
}

func (w *Window) Addr() (q0 int, q1 int, err error) {
	if w.addr == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/addr", w.id), proto.Ordwr)
		if err != nil {
			return 0, 0, fmt.Errorf("Tried to open addr file, but failed: %w", err)
		}
		w.addr = f
	}
	b := make([]byte, 100)
	n, err := w.addr.ReadAt(b, 0)
	if err != nil {
		return 0, 0, err
	}
	if n < 24 {
		return 0, 0, fmt.Errorf("Short read on addr file")
	}
	b = b[:n]
	q0s := b[:12]
	q1s := b[12:]
	q0, err = strconv.Atoi(strings.TrimSpace(string(q0s)))
	if err != nil {
		return 0, 0, err
	}
	q1, err = strconv.Atoi(strings.TrimSpace(string(q1s)))
	if err != nil {
		return 0, 0, err
	}
	return q0, q1, nil
}

func (w *Window) XData() (io.ReadWriter, error) {
	if w.data == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/xdata", w.id), proto.Ordwr)
		if err != nil {
			return nil, fmt.Errorf("Tried to open data file, but failed: %w", err)
		}
		w.data = f
	}
	return w.data, nil
}

func (w *Window) Tag() (string, error) {
	f, err := w.c.Open(fmt.Sprintf("/%s/tag", w.id), proto.Ordwr)
	if err != nil {
		return "", fmt.Errorf("Tried to open data file, but failed: %w", err)
	}
	defer f.Close()
	bs, err := ioutil.ReadAll(f)
	return string(bs), nil
}
