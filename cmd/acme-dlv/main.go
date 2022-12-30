package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/knusbaum/acmetools"

	"github.com/knusbaum/go9p"
	"github.com/knusbaum/go9p/client"
	"github.com/knusbaum/go9p/fs"
	"github.com/knusbaum/go9p/proto"
)

type AcmeSrv struct {
	log  chan string
	cmds chan string
}

func Serve() *AcmeSrv {
	user := "kjn"
	group := "kjn"

	logChan := make(chan string, 100)
	cmds := make(chan string, 100)

	logStream := fs.NewDroppingStream(8192)
	cmdStream := fs.NewDroppingStream(1024)
	acmeDlvFs, root := fs.NewFS(user, group, 0440)
	root.AddChild(fs.NewStreamFile(
		acmeDlvFs.NewStat("log", user, group, 0440),
		logStream,
	))
	root.AddChild(fs.NewStreamFile(
		acmeDlvFs.NewStat("cmd", user, group, 0220),
		cmdStream,
	))

	go func() {
		for {
			select {
			case l := <-logChan:
				io.WriteString(logStream, l)
			}
		}
	}()

	go func() {
		b := bufio.NewReader(cmdStream)
		for {
			cmd, err := b.ReadString('\n')
			if err != nil {
				log.Fatalf("Failed to read from command stream: %v", err)
			}
			select {
			case cmds <- strings.TrimSpace(cmd):
			default:
				log.Printf("Dropping command %s. Command queue full.", cmd)
			}
		}
	}()

	//go9p.Serve("localhost:9999", acmeDlvFs.Server())
	go func() {
		log.Fatalf("AcmeSrv shut down: %v", go9p.PostSrv("acme-dlv", acmeDlvFs.Server()))
	}()

	return &AcmeSrv{
		log:  logChan,
		cmds: cmds,
	}
}

func (s *AcmeSrv) Log(format string, a ...interface{}) {
	s.log <- fmt.Sprintf(format, a...)
}

func (s *AcmeSrv) Cmds() <-chan string {
	return s.cmds
}

var bp = flag.Bool("b", false, "Causes acme-dlv to send a breakpoint to an already running acme-dlv. Must be run on an acme window.")
var bpd = flag.Bool("d", false, "Opposite of -b, deletes a breakpoint. Must be run on an acme window.")
var xamine = flag.Bool("x", false, "Causes acme-dlv to examine a variable in a stopped acme-dlv session. Must be run on an acme window.")

func main() {
	flag.Parse()

	a, err := acmetools.NewAcme()
	if err != nil {
		log.Fatalf("Failed to connect to acme: %v", err)
	}

	if *bp {
		f, l, err := getFileLine(a)
		if err != nil {
			log.Fatalf("Failed to get line for breakpoint: %v", err)
		}
		//fmt.Printf(" GOT BREAK AT %s %v\n", f, l)
		err = writeCommand(fmt.Sprintf("BreakFile %s %d", f, l))
		if err != nil {
			log.Fatalf("Failed to write command: %v", err)
		}
		return
	}

	if *bpd {
		f, l, err := getFileLine(a)
		if err != nil {
			log.Fatalf("Failed to get line for breakpoint: %v", err)
		}
		//fmt.Printf(" GOT BREAK AT %s %v\n", f, l)
		err = writeCommand(fmt.Sprintf("DelBreakFile %s %d", f, l))
		if err != nil {
			log.Fatalf("Failed to write command: %v", err)
		}
		return
	}

	if *xamine {
		winid := os.Getenv("winid")
		if winid == "" {
			log.Fatalf("Could not find acme window. $winid not set.")
		}

		w, err := a.GetWindow(winid)
		if err != nil {
			//fmt.Printf("FATAL: %s\n", err)
			//os.Exit(1)
			log.Fatalf("Failed to get window: %v", err)
		}
		s, err := w.Selected()
		if err != nil {
			//fmt.Printf("FATAL: %s\n", err)
			//os.Exit(1)
			log.Fatalf("Failed to read selection: %v", err)
		}
		//fmt.Printf("SELECTED: [%s]\n", s)
		err = writeCommand(fmt.Sprintf("X %s", s))
		if err != nil {
			log.Fatalf("Failed to write command: %v", err)
		}
		return
	}

	as := Serve()

	// 	w, err := a.NewWindow()
	// 	if err != nil {
	// 		log.Fatalf("Failed to create dlv window: %v", err)
	// 	}
	// 	es, err := w.Events()
	// 	if err != nil {
	// 		log.Fatalf("Failed to get events stream: %v", err)
	// 	}
	// 	defer es.Close()
	// 	go func() {
	// 		for e := range es.C {
	// 			fmt.Printf("Event: [%s]\n", e)
	// 		}
	// 		fmt.Printf("EVENT CHANNEL CLOSED!\n")
	// 	}()

	for {
		select {
		case cmd := <-as.Cmds():
			// 			a.Log("Got Command: [%s]\n", cmd)
			// 			as.Log("Got Command: [%s]\n", cmd)

			if strings.HasPrefix(cmd, "New") {
				arg := strings.TrimSpace(strings.TrimPrefix(cmd, "New"))
				fi, err := os.Stat(arg)
				if err != nil {
					a.Log("Failed to find directory %s: %v\n", arg, err)
					continue
				}
				if !fi.IsDir() {
					a.Log("%s is not a directory.\n", arg)
					continue
				}
				RunDlvWin(a, arg, as.Cmds())
			} else {
				a.Log("[%s]\nNo active debug session running. Please start one before setting breakpoints.\n", cmd)
			}
		}
	}
}

func RunDlvWin(a *acmetools.Acme, dir string, cmds <-chan string) {
	defer log.Printf("Shut down DLV window for %s\n", dir)
	win, err := a.NewWindow()
	if err != nil {
		a.Log("Failed to create dlv window: %v", err)
		return
	}
	win.AppendTag(fmt.Sprintf("| (Debugging Tests %s)\nRestart Continue Stop\nBreaks\tDelBreak\nNext\tStep\tX", dir))
	defer win.AppendTag("(DEFUNCT)")

	body, err := win.Body()
	if err != nil {
		a.Log("Failed to write to body: %v\n", err)
		return
	}

	es, err := win.Events()
	if err != nil {
		fmt.Fprintf(body, "Failed to get events stream: %v\n", err)
		return
	}
	defer es.Close()

	port, err := LaunchTestDlv(dir, body, body)
	if err != nil {
		a.Log("Failed to launch Delve Test: %v", err)
		return
	}

	c := rpc2.NewClient(fmt.Sprintf("localhost:%d", port))
	defer c.Disconnect(false)

	handleDebuggerState := func(s *api.DebuggerState) {
		//fmt.Printf("GOT STATE: ")
		spew.Dump(s)
		if s.CurrentThread != nil {
			bp := s.CurrentThread.Breakpoint
			if bp != nil {
				if bp.ID == -1 {
					// This is a panic or other non-user break.
					//fmt.Printf("PANIC\n")
					frames, err := c.Stacktrace(s.CurrentThread.GoroutineID, 1000, api.StacktraceSimple, &api.LoadConfig{
						FollowPointers:     false,
						MaxVariableRecurse: 0,
						MaxStringLen:       20,
						MaxArrayValues:     5,
						MaxStructFields:    0,
					})
					if err != nil {
						fmt.Fprintf(body, "Failed to get stacktrace: %v\n", err)
					}
					//spew.Dump(frames)
					//api.PrintStack(func(s string) string { return s }, os.Stdout, frames, "ind", true, func(api.Stackframe) bool { return true })
					for _, fr := range frames {
						fmt.Fprintf(body, "At 0x%00X in %s\n", fr.PC, fr.Function.Name())
						fmt.Fprintf(body, "\tat %s:%d\n", fr.File, fr.Line)
					}
				} else {
					err = acmetools.PlumbCmd(dir, fmt.Sprintf("%s:%d", bp.File, bp.Line))
					if err != nil {
						fmt.Fprintf(body, "Failed to plumb: %v\n", err)
					}
				}
			} else {
				err = acmetools.PlumbCmd(dir, fmt.Sprintf("%s:%d", s.CurrentThread.File, s.CurrentThread.Line))
				if err != nil {
					fmt.Fprintf(body, "Failed to plumb: %v\n", err)
				}
			}
			vars, err := c.ListLocalVariables(api.EvalScope{GoroutineID: s.CurrentThread.GoroutineID}, api.LoadConfig{
				FollowPointers:     false,
				MaxVariableRecurse: 0,
				MaxStringLen:       20,
				MaxArrayValues:     5,
				MaxStructFields:    0,
			})
			if err != nil {
				fmt.Fprintf(body, "Error getting local vars: %v\n", err)
			} else {
				fmt.Fprintf(body, "Vars:\n")
				for _, v := range vars {
					//					fmt.Printf("\t%s (%s): %s\n", v.Name, v.Type, v.Value)
					fmt.Fprintf(body, "\t%s = %s\n", v.Name, v.SinglelineString())
				}
			}
		}

		if s.Err != nil {
			fmt.Fprintf(body, "DLV Error: %v\n", s.Err)
		}
	}

	handleCommand := func(next <-chan *api.DebuggerState, cmd string) <-chan *api.DebuggerState {
		fmt.Printf("Handling [%s]\n", cmd)
		if cmd == "Continue" {
			fmt.Fprintf(body, "Continuing\n")
			c := c.Continue()
			return c
		}

		if cmd == "Restart" {
			fmt.Fprintf(body, "Restarting\n")
			bps, err := c.Restart(true)
			if err != nil {
				fmt.Fprintf(body, "Failed to restart target: %v\n", err)
				return next
			}
			for _, bp := range bps {
				fmt.Fprintf(body, "Removed %s:%d : %s\n", bp.Breakpoint.File, bp.Breakpoint.Line, bp.Reason)
			}
			return next
		}

		if strings.HasPrefix(cmd, "BreakFile ") {
			args := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(cmd, "BreakFile")), " ", 2)
			line, err := strconv.Atoi(strings.TrimSpace(args[1]))
			if err != nil {
				fmt.Fprintf(body, "Failed to parse breakpoint: %v\n", err)
				fmt.Fprintf(body, "\t[%v]\n", cmd)
				return next
			}
			fname := strings.TrimSpace(args[0])
			// 			fmt.Fprintf(body, "Cmd: %v\n", cmd)
			// 			fmt.Fprintf(body, "\t[%v]\n", args)
			// 			fmt.Fprintf(body, "\t[%v]\n", line)
			bp, err := c.CreateBreakpoint(&api.Breakpoint{
				//Name: fmt.Sprintf("%s:%d", fname, line),
				File: fname,
				Line: line,
			})
			if err != nil {
				fmt.Fprintf(body, "Failed to set breakpoint: %v\n", err)
				return next
			}
			fmt.Fprintf(body, "Breakpoint set: %s:%d\n", bp.File, bp.Line)
			return next
		}

		if strings.HasPrefix(cmd, "DelBreakFile ") {
			args := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(cmd, "DelBreakFile")), " ", 2)
			line, err := strconv.Atoi(strings.TrimSpace(args[1]))
			if err != nil {
				fmt.Fprintf(body, "Failed to parse breakpoint: %v\n", err)
				fmt.Fprintf(body, "\t[%v]\n", cmd)
				return next
			}
			fname := strings.TrimSpace(args[0])
			// 			fmt.Fprintf(body, "Cmd: %v\n", cmd)
			// 			fmt.Fprintf(body, "\t[%v]\n", args)
			// 			fmt.Fprintf(body, "\t[%v]\n", line)
			bps, err := c.ListBreakpoints(false)
			if err != nil {
				fmt.Fprintf(body, "Failed to list breakpoints: %v\n", err)
				return next
			}
			for _, bp := range bps {
				if fmt.Sprintf("%s:%d", fname, line) == fmt.Sprintf("%s:%d", bp.File, bp.Line) {
					//fmt.Fprintf(body, "Deleting breakpoint %d\n", bp.ID)
					//c.ClearBreakpointByName(fmt.Sprintf("%s:%d", fname, line))
					bp, err := c.ClearBreakpoint(bp.ID)
					if err != nil {
						fmt.Fprintf(body, "Failed to clear breakpoint: %v\n", err)
						return next
					}
					fmt.Fprintf(body, "Breakpoint %d cleared: %s:%d\n", bp.ID, bp.File, bp.Line)
					return next
				}
			}
			fmt.Fprintf(body, "Breakpoint not found.\n")
			return next
		}

		if strings.HasPrefix(cmd, "DelBreak ") {
			arg := strings.TrimSpace(strings.TrimPrefix(cmd, "DelBreak "))
			bpn, err := strconv.Atoi(arg)
			if err != nil {
				fmt.Fprintf(body, "Expected a breakpoint number, but found \"%v\": %v\n", arg, err)
			}
			bp, err := c.ClearBreakpoint(bpn)
			if err != nil {
				fmt.Fprintf(body, "Failed to clear breakpoint: %v\n", err)
				return next
			}
			fmt.Fprintf(body, "Breakpoint cleared: %s:%d\n", bp.File, bp.Line)
		}

		if strings.HasPrefix(cmd, "Breaks") {
			bps, err := c.ListBreakpoints(false)
			if err != nil {
				fmt.Fprintf(body, "Failed to list breakpoints: %v\n", err)
				return next
			}
			fmt.Printf("Breakpoints:\n")
			for _, bp := range bps {
				fmt.Fprintf(body, "(%d): %s\n", bp.ID, bp.Name)
				fmt.Fprintf(body, "\t(0x%016X): %s\n", bp.Addr, bp.FunctionName)
				fmt.Fprintf(body, "\t%s:%d\n", bp.File, bp.Line)
			}
		}

		if strings.HasPrefix(cmd, "Stop") {
			ds, err := c.Halt()
			if err != nil {
				fmt.Fprintf(body, "Failed to halt.\n")
			}
			handleDebuggerState(ds)
		}
		if strings.HasPrefix(cmd, "Next") {
			ds, err := c.Next()
			if err != nil {
				fmt.Fprintf(body, "Failed to next.\n")
			}
			handleDebuggerState(ds)
		}
		if strings.HasPrefix(cmd, "Step") {
			ds, err := c.Step()
			if err != nil {
				fmt.Fprintf(body, "Failed to step.\n")
			}
			handleDebuggerState(ds)
		}
		if strings.HasPrefix(cmd, "X ") {
			arg := strings.TrimSpace(strings.TrimPrefix(cmd, "X "))
			s, err := c.GetState()
			if err != nil {
				fmt.Fprintf(body, "Failed to get debugger state: %v\n", err)
				return next
			}
			if s.CurrentThread == nil {
				fmt.Fprintf(body, "Failed to get current thread. It is nil.\n")
				return next
			}
			v, err := c.EvalVariable(api.EvalScope{GoroutineID: s.CurrentThread.GoroutineID}, arg, api.LoadConfig{
				FollowPointers:     true,
				MaxVariableRecurse: 1000,
				MaxStringLen:       2000,
				MaxArrayValues:     50,
				MaxStructFields:    100,
			})
			if err != nil {
				fmt.Fprintf(body, "Error getting local vars: %v\n", err)
				return next
			}
			fmt.Fprintf(body, "\t%s = %s\n", v.Name, v.MultilineString("\t", ""))

			// 			vars, err := c.ListLocalVariables(api.EvalScope{GoroutineID: s.CurrentThread.GoroutineID}, api.LoadConfig{
			// 				FollowPointers:     true,
			// 				MaxVariableRecurse: 1000,
			// 				MaxStringLen:       2000,
			// 				MaxArrayValues:     50,
			// 				MaxStructFields:    100,
			// 			})
			// 			if err != nil {
			// 				fmt.Fprintf(body, "Error getting local vars: %v\n", err)
			// 				return next
			// 			}
			// 			var found bool
			// 			for _, v := range vars {
			// 				//					fmt.Printf("\t%s (%s): %s\n", v.Name, v.Type, v.Value)
			// 				if v.Name == arg {
			// 					found = true
			// 					fmt.Fprintf(body, "\t%s = %s\n", v.Name, v.MultilineString("\t", ""))
			// 				}
			// 			}
			// 			if !found {
			// 				fmt.Fprintf(body, "no such variable [%s] found.\nHave:\n", arg)
			// 				for _, v := range vars {
			// 					fmt.Fprintf(body, "\t%s\n", v.Name)
			// 				}
			// 			}
		}

		return next
	}

	handleEvent := func(next <-chan *api.DebuggerState, e *acmetools.Event) <-chan *api.DebuggerState {
		fmt.Printf("Event: [%+v]\n", e)
		if e.HasExpansion() {
			nexte := <-es.C
			fmt.Printf("NEXT PART: [%+v]\n", nexte)
			e.NChars = nexte.NChars
			e.S = nexte.S
			fmt.Printf("FinalEvent: [%+v]\n", e)
		}
		fmt.Printf("FinalEvent2: [%#v]\n", e)
		fmt.Printf("FLAG: %v\n", e.Flag)
		if e.IsBuiltin() {
			fmt.Printf("Writing Back.\n")
			es.WriteBack(e)
			return next
		}

		if e.Type == acmetools.ET_BodyBtn2 || e.Type == acmetools.ET_TagBtn2 {
			return handleCommand(next, e.S)
		}
		return next
	}

	var next <-chan *api.DebuggerState
	for {
		if next != nil {
			//fmt.Printf("Waiting on EVENT and NEXT too.\n")
			select {
			case e, ok := <-es.C:
				if !ok {
					return
				}
				next = handleEvent(next, e)
			case cmd, ok := <-cmds:
				if !ok {
					a.Log("Command Channel closed. Exiting.\n")
					return
				}
				next = handleCommand(next, cmd)
			case s := <-next:
				next = nil
				handleDebuggerState(s)
			}
		} else {
			//fmt.Printf("Waiting on EVENT only.\n")
			select {
			case e, ok := <-es.C:
				if !ok {
					return
				}
				next = handleEvent(next, e)
			case cmd, ok := <-cmds:
				if !ok {
					a.Log("Command Channel closed. Exiting.\n")
					return
				}
				next = handleCommand(next, cmd)
			}
		}
	}
}

var port int = 35800

func genport() int {
	port++
	return port
}

func testRPC() {
	c := rpc2.NewClient("localhost:8181")
	// 	if err != nil {
	// 		log.Fatalf("Failed to dial client: %v", err)
	// 	}
	bp, err := c.CreateBreakpoint(&api.Breakpoint{
		File: "/home/kjn/CodeBase/datadog-agent/pkg/trace/agent/agent_test.go",
		Line: 63,
	})
	if err != nil {
		log.Fatalf("Failed to create breakpoint: %v", err)
	}
	//spew.Dump(bp)
	log.Printf("Created breakpoint in %s", bp.FunctionName)

	state := <-c.Continue()
	spew.Dump(state)
}

func LaunchTestDlv(dir string, stdout, stderr io.Writer) (int, error) {
	port := genport()
	c := exec.Command("dlv", "test", "--headless", "-l", fmt.Sprintf("127.0.0.1:%d", port))
	c.Dir = dir
	c.Stdout = stdout
	c.Stderr = stderr
	fmt.Printf("Starting: %v\n", c)
	err := c.Start()
	if err != nil {
		return 0, err
	}
	time.Sleep(1 * time.Second)
	return port, nil
}

func getFileLine(a *acmetools.Acme) (string, int, error) {
	winid := os.Getenv("winid")
	if winid == "" {
		return "", 0, fmt.Errorf("Could not find acme window. $winid not set.")
	}

	w, err := a.GetWindow(winid)
	if err != nil {
		//fmt.Printf("FATAL: %s\n", err)
		//os.Exit(1)
		return "", 0, err
	}

	tag, err := w.Tag()
	if err != nil {
		//		fmt.Printf("FATAL: %s\n", err)
		//		os.Exit(1)
		return "", 0, err
	}
	parts := strings.SplitN(tag, " ", 2)
	fname := parts[0]
	stat, err := os.Stat(fname)
	if err != nil {
		//		fmt.Printf("FATAL: %s: %s\n", fname, err)
		//		os.Exit(1)
		return "", 0, fmt.Errorf("%s: %w", fname, err)
	}

	lineStart := 1
	lineEnd := 1
	if !stat.IsDir() {
		// Find the line number
		lineStart, lineEnd, err = w.LineNumber()
		if err != nil {
			//			fmt.Printf("FATAL: %s\n", err)
			//			os.Exit(1)
			return "", 0, err
		}
	}

	//	fname, lineStart, lineEnd
	if lineEnd != lineStart {
		//fmt.Printf("FATAL: can only break on one line at a time.\n")
		//		os.Exit(1)
		return "", 0, fmt.Errorf("can only break on one line at a time.")
	}

	return fname, lineStart, nil
}

func writeCommand(s string) error {
	//fmt.Printf("WRITING COMMAND %v\n", s)
	u, err := user.Current()
	if err != nil {
		return err
	}
	ns, err := acmetools.Namespace()
	if err != nil {
		return fmt.Errorf("Can't locate namespace: %w", err)
	}
	acmef, err := net.Dial("unix", path.Join(ns, "acme-dlv"))
	if err != nil {
		return fmt.Errorf("Failed to dial acme-dlv: %w", err)
	}
	npc, err := client.NewClient(acmef, u.Username, "")
	if err != nil {
		return fmt.Errorf("Failed to attach to acme-dlv: %w", err)
	}
	// TODO: add this when go9p adds Close to client
	//defer npc.Close()

	f, err := npc.Open("/cmd", proto.Owrite)
	if err != nil {
		return err
	}
	defer f.Close()
	//fmt.Printf("WRITING [%s]\n", s)
	fmt.Fprintf(f, "%s\n", s)
	return nil
}
