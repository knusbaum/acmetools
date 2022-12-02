package acmetools

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
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

type EventStream struct {
	C chan *Event
	f *client.File
}

type Origin int

const (
	EV_Write Origin = iota
	EV_File
	EV_Keyboard
	EV_Mouse
)

func parseOrigin(r rune) Origin {
	switch r {
	case 'E':
		return EV_Write
	case 'F':
		return EV_File
	case 'K':
		return EV_Keyboard
	case 'M':
		return EV_Mouse
	}
	return Origin(-1)
}

func (o Origin) String() string {
	switch o {
	case EV_Write:
		return "EV_Write"
	case EV_File:
		return "EV_File"
	case EV_Keyboard:
		return "EV_Keyboard"
	case EV_Mouse:
		return "EV_Mouse"
	default:
		return "UNKNOWN EVENT ORIGIN"
	}
}

type EType int

const (
	ET_BodyDelete EType = iota
	ET_TagDelete
	ET_BodyInsert
	ET_TagInsert
	ET_BodyBtn3
	ET_TagBtn3
	ET_BodyBtn2
	ET_TagBtn2
)

func parseEType(r rune) EType {
	switch r {
	case 'D':
		return ET_BodyDelete
	case 'd':
		return ET_TagDelete
	case 'I':
		return ET_BodyInsert
	case 'i':
		return ET_TagInsert
	case 'L':
		return ET_BodyBtn3
	case 'l':
		return ET_TagBtn3
	case 'X':
		return ET_BodyBtn2
	case 'x':
		return ET_TagBtn2
	}
	return EType(-1)
}

func (t EType) String() string {
	switch t {
	case ET_BodyDelete:
		return "ET_BodyDelete"
	case ET_TagDelete:
		return "ET_TagDelete"
	case ET_BodyInsert:
		return "ET_BodyInsert"
	case ET_TagInsert:
		return "ET_TagInsert"
	case ET_BodyBtn3:
		return "ET_BodyBtn3"
	case ET_TagBtn3:
		return "ET_TagBtn3"
	case ET_BodyBtn2:
		return "ET_BodyBtn2"
	case ET_TagBtn2:
		return "ET_TagBtn2"
	default:
		return "UNKNOWN EVENT TYPE"
	}
}

type Event struct {
	Origin    Origin
	Type      EType
	StartAddr int
	EndAddr   int
	Flag      int
	NChars    int
	S         string
}

func parseEvent(r *bufio.Reader) (*Event, error) {
	// The messages have a fixed format: a character indicating the origin or cause of the action, a
	// character indicating the type of the action, four free-format blank-terminated decimal numbers,
	// optional text, and a newline.  The first and second numbers are the character addresses of the
	// action, the third is a flag, and the final is a count of the characters in the optional text,
	// which may itself contain newlines.  The origin characters are E for writes to the body or tag
	// file, F for actions through the window's other files, K for the keyboard, and M for the mouse.
	// The type characters are D for text deleted from the body, d for text deleted from the tag, I for
	// text inserted to the body, i for text inserted to the tag, L for a button 3 action in the body,
	// l for a button 3 action in the tag, X for a button 2 action in the body, and x for a button 2
	// action in the tag.
	//
	// If the relevant text has less than 256 characters, it is included in the message; otherwise it
	// is elided, the fourth number is 0, and the program must read it from the data file if needed. No
	// text is sent on a D or d message.
	//
	// For D, d, I, and i the flag is always zero.  For X and x, the flag is a bitwise OR (reported
	// decimally) of the following: 1 if the text indicated is recognized as an acme built-in command;
	// 2 if the text indicated is a null string that has a non-null expansion; if so, another complete
	// message will follow describing the expansion exactly as if it had been indicated explicitly (its
	// flag will always be 0); 8 if the com- mand has an extra (chorded) argument; if so, two more
	// complete messages will follow reporting the argument (with all numbers 0 except the character
	// count) and where it originated, in the form of a fully-qualified button 3 style address.
	//
	// For L and l, the flag is the bitwise OR of the follow- ing: 1 if acme can interpret the action
	// without loading a new file; 2 if a second (post-expansion) message fol- lows, analogous to that
	// with X messages; 4 if the text is a file or window name (perhaps with address) rather than plain
	// literal text.
	//
	// For messages with the 1 bit on in the flag, writing the message back to the event file, but with
	// the flag, count, and text omitted, will cause the action to be applied to the file exactly as it
	// would have been if the event file had not been open.
	var b [2]byte
	_, err := r.Read(b[:])
	if err != nil {
		return nil, err
	}
	origin := parseOrigin(rune(b[0]))
	aType := parseEType(rune(b[1]))

	//nums := strings.SplitN(l[2:], " ", 5)
	//s := strings.TrimString(r.ReadString(' '))
	s, err := r.ReadString(' ')
	if err != nil {
		return nil, err
	}
	saddr, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(err)
	}
	s, err = r.ReadString(' ')
	if err != nil {
		return nil, err
	}
	eaddr, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(err)
	}
	s, err = r.ReadString(' ')
	if err != nil {
		return nil, err
	}
	flag, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(err)
	}
	s, err = r.ReadString(' ')
	if err != nil {
		return nil, err
	}
	nchars, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(err)
	}

	bs := make([]byte, nchars)
	_, err = r.Read(bs)
	if err != nil {
		return nil, err
	}
	r.ReadByte()
	return &Event{
		Origin:    origin,
		Type:      aType,
		StartAddr: saddr,
		EndAddr:   eaddr,
		Flag:      flag,
		NChars:    nchars,
		S:         string(bs),
	}, nil
}

func (e *EventStream) Close() error {
	fmt.Printf("Closing Event Stream.\n")
	return e.f.Close()
}

func NewAcme() (*Acme, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	ns, err := Namespace()
	if err != nil {
		return nil, fmt.Errorf("Can't locate namespace: %w", err)
	}
	acmef, err := net.Dial("unix", path.Join(ns, "acme"))
	if err != nil {
		return nil, fmt.Errorf("Failed to dial acme: %w", err)
	}
	npc, err := client.NewClient(acmef, u.Username, "")
	if err != nil {
		return nil, fmt.Errorf("Failed to attach to acme: %w", err)
	}
	return &Acme{npc}, nil
}

func (a *Acme) NewWindow() (*Window, error) {
	w := &Window{c: a.c}
	f, err := w.c.Open("/new/ctl", proto.Ordwr)
	if err != nil {
		return nil, fmt.Errorf("Tried to open ctl file, but failed: %w", err)
	}
	w.ctl = f
	ps, err := w.ReadCtl()
	if err != nil {
		return nil, err
	}
	w.id = fmt.Sprintf("%d", ps.ID)
	return w, nil
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

func (w *Window) Events() (*EventStream, error) {
	c := make(chan *Event, 100)

	f, err := w.c.Open(fmt.Sprintf("/%s/event", w.id), proto.Ordwr)
	if err != nil {
		return nil, fmt.Errorf("Tried to open event file, but failed: %w", err)
	}

	go func() {
		defer fmt.Printf("Shutting down event stream.\n")
		defer close(c)
		r := bufio.NewReader(f)
		for {
			// 			s, err := r.ReadString('\n')
			// 			if err != nil {
			// 				log.Printf("Failed to read events file: %v", err)
			// 				return
			// 			}
			e, err := parseEvent(r)
			if err != nil {
				log.Printf("Failed to read events file: %v", err)
				return
			}
			c <- e
		}
	}()

	return &EventStream{C: c, f: f}, nil
}

type WinParams struct {
	ID        int
	TagChars  int
	BodyChars int
	Dir       bool
	Modified  bool
}

func parseWinParams(s string) (WinParams, error) {
	// holds 5 decimal numbers, each formatted in 11
	// characters plus a blank-the window ID; number of char-
	// acters (runes) in the tag; number of characters in the
	// body; a 1 if the window is a directory, 0 otherwise;
	// and a 1 if the window is modified, 0 otherwise-followed
	// by the tag up to a newline if present.  Thus at charac-
	// ter position 5×12 starts the name of the window.

	if len(s) < 60 {
		return WinParams{}, fmt.Errorf("Win params string input too short.")
	}
	window_id, err := strconv.Atoi(strings.TrimSpace(s[:12]))
	if err != nil {
		return WinParams{}, err
	}
	ntagchars, err := strconv.Atoi(strings.TrimSpace(s[13:24]))
	if err != nil {
		return WinParams{}, err
	}
	nbodychars, err := strconv.Atoi(strings.TrimSpace(s[25:36]))
	if err != nil {
		return WinParams{}, err
	}
	dir, err := strconv.Atoi(strings.TrimSpace(s[37:48]))
	if err != nil {
		return WinParams{}, err
	}
	mod, err := strconv.Atoi(strings.TrimSpace(s[49:60]))
	if err != nil {
		return WinParams{}, err
	}
	return WinParams{
		ID:        window_id,
		TagChars:  ntagchars,
		BodyChars: nbodychars,
		Dir:       dir > 0,
		Modified:  mod > 0,
	}, nil
}

func (w *Window) ReadCtl() (WinParams, error) {
	if w.ctl == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/ctl", w.id), proto.Ordwr)
		if err != nil {
			return WinParams{}, fmt.Errorf("Tried to open ctl file, but failed: %w", err)
		}
		w.ctl = f
	}

	bs, err := io.ReadAll(w.ctl)
	if err != nil {
		return WinParams{}, err
	}
	return parseWinParams(string(bs))
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

func (w *Window) lnFromSel() (int, error) {
	xdata, err := w.XData()
	if err != nil {
		return 0, err
	}
	line := 1
	r := bufio.NewReader(xdata)
	for {
		_, err := r.ReadSlice('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		line++
	}
	return line, nil
}

func (w *Window) LineNumber() (l0 int, l1 int, err error) {
	// Make sure addr is open
	_, _, err = w.Addr()
	if err != nil {
		return 0, 0, err
	}
	// For some reason addr=dot is required before a successful WriteAddr.
	// Not sure why
	err = w.Ctl("addr=dot")
	if err != nil {
		return 0, 0, err
	}
	err = w.WriteAddr("0,.-")
	if err != nil {
		return 0, 0, err
	}
	l0, err = w.lnFromSel()
	if err != nil {
		return 0, 0, err
	}

	// For some reason addr=dot is required before a successful WriteAddr.
	// Not sure why
	err = w.Ctl("addr=dot")
	if err != nil {
		return 0, 0, err
	}
	err = w.WriteAddr("0,.")
	if err != nil {
		return 0, 0, err
	}
	l1, err = w.lnFromSel()
	if err != nil {
		return 0, 0, err
	}
	return l0, l1, nil
}

func Plumb(s string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	ns, err := Namespace()
	if err != nil {
		return fmt.Errorf("Can't locate namespace: %w", err)
	}
	acmef, err := net.Dial("unix", path.Join(ns, "plumb"))
	if err != nil {
		return fmt.Errorf("Failed to dial plumber: %w", err)
	}
	npc, err := client.NewClient(acmef, u.Username, "")
	if err != nil {
		return fmt.Errorf("Failed to attach to acme: %w", err)
	}
	// TODO: add this when go9p adds Close to client
	//defer npc.Close()

	f, err := npc.Open("/send", proto.Owrite)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "acmetools\nweb\n/\ntext\n\n%d\n%s", len(s), s)
	if err != nil {
		return err
	}
	return nil
}
