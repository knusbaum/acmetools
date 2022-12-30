package acmetools

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/knusbaum/go9p/client"
	"github.com/knusbaum/go9p/proto"
)

// Namespace duplicates plan9port's getns behavior, returning the local namespace directory
// where plan9 services may be posted.
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

// Acme represents a connection to an Acme/Edwood instance.
type Acme struct {
	c    *client.Client
	cons *client.File
}

// NewAcme creates a connection to a running Acme/Edwood instance by looking for
// the service `acme` in the current namespace (See: Namespace()).
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
	return &Acme{npc, nil}, nil
}

// NewWindow will open a new Window.
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

// GetWindow returns a handle to an existing window by its numeric ID
func (a *Acme) GetWindow(id string) (*Window, error) {
	_, err := a.c.Stat("/" + id)
	if err != nil {
		return nil, err
	}
	return &Window{c: a.c, id: id}, nil
}

// Log accepts a format string and arguments, which will be formatted according to the fmt package.
// This will be written to a window labeled `+Errors`.
func (a *Acme) Log(f string, args ...interface{}) error {
	if a.cons == nil {
		f, err := a.c.Open("/cons", proto.Ordwr)
		if err != nil {
			return fmt.Errorf("Tried to open cons file, but failed: %w", err)
		}
		a.cons = f
	}
	_, err := fmt.Fprintf(a.cons, f, args...)
	return err
}

// Window represents an Acme window
type Window struct {
	c  *client.Client
	id string

	addr *client.File
	ctl  *client.File
	//data *client.File
	body *client.File
}

// EventStream represents a stream of events from a Window. These events are read from the window's
// `event` file. Events should be read from the chan C.
type EventStream struct {
	C chan *Event
	f *client.File
}

// Origin is used to identify the source of an Event.
type Origin int

const (
	// The event was caused by a write to the body or tag file.
	EV_Write Origin = iota
	// The event was caused by a write to one of the window's files other than body or tag.
	EV_File
	// The event was caused by the keyboard.
	EV_Keyboard
	// The event was caused by the mouse.
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

// Char returns the character associated with the event origin. This character
// is specified by acme(4) under the event file description.
func (o Origin) Char() rune {
	switch o {
	case EV_Write:
		return 'E'
	case EV_File:
		return 'F'
	case EV_Keyboard:
		return 'K'
	case EV_Mouse:
		return 'M'
	}
	return '?'
}

// String returns a human-readable string representing the Origin. This is different
// from the character that represents the event origin. For the acme(4) character that
// identifies the origin, see Char().
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

// EType is the type of an event.
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

// Char returns the character associated with the event type. This character
// is specified by acme(4) under the event file description.
func (t EType) Char() rune {
	switch t {
	case ET_BodyDelete:
		return 'D'
	case ET_TagDelete:
		return 'd'
	case ET_BodyInsert:
		return 'I'
	case ET_TagInsert:
		return 'i'
	case ET_BodyBtn3:
		return 'L'
	case ET_TagBtn3:
		return 'l'
	case ET_BodyBtn2:
		return 'X'
	case ET_TagBtn2:
		return 'x'
	}
	return '?'
}

// String returns a human-readable string representing the EType. This is different
// from the character that represents the event type. For the acme(4) character that
// identifies the event type, see Char().
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

// Event is something that happens in a window. This includes insertions, deletions, keypresses, clicks, etc.
type Event struct {
	Origin    Origin
	Type      EType
	StartAddr int
	EndAddr   int
	Flag      int
	NChars    int
	S         string
}

// parseEvent reads an event from the event file, returning an Event.
//
// See: acme(4)
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
func parseEvent(r *bufio.Reader) (*Event, error) {
	var b [2]byte
	_, err := r.Read(b[:])
	if err != nil {
		return nil, err
	}
	origin := parseOrigin(rune(b[0]))
	aType := parseEType(rune(b[1]))

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

// IsBuiltin reports whether an event has a built-in implementation of the command.
// This can be used to determine if WriteBack() can be used to execute the event.
// This includes built-in commands like Del, Put, Get, etc.
// This is only true if acme can execute the event without loading a new file.
func (e *Event) IsBuiltin() bool {
	return e.Flag&0x1 != 0
}

// HasExpansion reports whether this event has an expansion, meaning there will be another message
// following that contains the expanded text.
//
// For example, a single button-2 click on Look will send an event with S=="" and HasExpansion() == true
// followed by another event with S == "Look" and HasExpansion() == false.
func (e *Event) HasExpansion() bool {
	return e.Flag&0x2 != 0
}

// Chorded reports whether the event represents a chorded command. If so, 2 more events should
// follow reporting the argument: The first will contain only the character count, and the second
// will contain where it originated, in the form of a fully-qualified button 3 style address.
func (e *Event) Chorded() bool {
	return e.Flag&0x8 != 0
}

// String prints a human-readable representation of the event.
func (e *Event) String() string {
	return fmt.Sprintf("%c%c%d %d %d %d %s", e.Origin.Char(), e.Type.Char(), e.StartAddr, e.EndAddr, e.Flag, e.NChars, e.S)
}

// This causes the event to be written back to the event file, according to acme(4).
//
// For events where IsBuiltin() == true, writing the message back will cause the action to be
// applied to the file exactly as it would have been if the event file had not been open.
func (e *EventStream) WriteBack(ev *Event) error {
	_, err := fmt.Fprintf(e.f, "%c%c%d %d\n", ev.Origin.Char(), ev.Type.Char(), ev.StartAddr, ev.EndAddr)
	return err
}

// Close closes the event stream, returning control of the window to Acme.
func (e *EventStream) Close() error {
	fmt.Printf("Closing Event Stream.\n")
	return e.f.Close()
}

// Close will close the Window.
func (w *Window) Close() error {
	if w.addr != nil {
		w.addr.Close()
		w.addr = nil
	}
	if w.ctl != nil {
		w.ctl.Close()
		w.ctl = nil
	}
	return nil
}

// Events returns an EventStream which can be used by applications to handle
// window events. Please see EventStream and acme(4) for more details.
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

// WinParams represents the 5 parameters read from the Window's ctl file.
type WinParams struct {
	ID        int  // The Window's ID
	TagChars  int  // The number of characters in the tag
	BodyChars int  // The number of characters in the body
	Dir       bool // True if the window is a directory
	Modified  bool // True if the window has been modified
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

// ReadCtl reads the window's ctl file and returns the WinParams associated with the Window.
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

// Ctl writes a message to the Window's ctl file. Ctl will add a newline to the message. This can
// be used to control much of the window's state. See acme(4) for what messages can be written.
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

// WriteAddr will write an address to the Window's addr file. This can be any format
// understood by button 3 (but without the initial colon). This affects what data will
// be read from the Data() and XData() files.
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

// Addr reads the window's addr file and returns the value of the address that would next be read
// or written through the data file in the format of 2 character (not byte) offsets. (See: acme(4))
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

// XData returns a new handle to the window's xdata file, which will return data
// according to the address set by WriteAddr.
func (w *Window) XData() (io.ReadWriteCloser, error) {
	//if w.data == nil {
	f, err := w.c.Open(fmt.Sprintf("/%s/xdata", w.id), proto.Ordwr)
	if err != nil {
		return nil, fmt.Errorf("Tried to open data file, but failed: %w", err)
	}
	//w.data = f
	//}
	//return w.data, nil
	return f, nil
}

// Tag returns the window's tag.
func (w *Window) Tag() (string, error) {
	f, err := w.c.Open(fmt.Sprintf("/%s/tag", w.id), proto.Ordwr)
	if err != nil {
		return "", fmt.Errorf("Tried to open data file, but failed: %w", err)
	}
	defer f.Close()
	bs, err := ioutil.ReadAll(f)
	return string(bs), nil
}

// AppendTag adds a string to the end of the window's tag
func (w *Window) AppendTag(s string) error {
	f, err := w.c.Open(fmt.Sprintf("/%s/tag", w.id), proto.Ordwr)
	if err != nil {
		return fmt.Errorf("Tried to open data file, but failed: %w", err)
	}
	defer f.Close()
	_, err = io.WriteString(f, s)
	return err
}

func (w *Window) lnFromSel() (int, error) {
	xdata, err := w.XData()
	if err != nil {
		return 0, err
	}
	defer xdata.Close()
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

// LineNumber returns the start and end line numbers of the user's currently selected text.
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

// Selected returns the currently selected text.
func (w *Window) Selected() (string, error) {
	_, _, err := w.Addr()
	if err != nil {
		return "", err
	}
	// For some reason addr=dot is required before a successful WriteAddr.
	// Not sure why
	err = w.Ctl("addr=dot")
	if err != nil {
		return "", err
	}
	xd, err := w.XData()
	if err != nil {
		return "", fmt.Errorf("Failed to read selected text: %w", err)
	}
	defer xd.Close()
	bs, err := io.ReadAll(xd)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

// Body returns the window's body file.
func (w *Window) Body() (io.ReadWriter, error) {
	if w.body == nil {
		f, err := w.c.Open(fmt.Sprintf("/%s/body", w.id), proto.Ordwr)
		if err != nil {
			return nil, fmt.Errorf("Tried to open body file, but failed: %w", err)
		}
		w.body = f
	}
	return w.body, nil
}

// PlumbCmd invokes the plumb command on s in the directory dir.
func PlumbCmd(dir string, s string) error {
	c := exec.Command("plumb", s)
	c.Dir = dir
	return c.Run()
}

// Plumb sends a plumbing message to the plumber, according to plumb(7).
// src:  application/service generating message
// dst:  destination `port' for message
// wdir: working directory (used if data is a file name)
// data: the data itself
// See: plumb(7) for details.
func Plumb(src, dest, wdir, data string) error {
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

	_, err = fmt.Fprintf(f, "%s\n%s\n%s\ntext\n\n%d\n%s", src, dest, wdir, len(data), data)
	if err != nil {
		return err
	}
	return nil
}
