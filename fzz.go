package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"unicode/utf8"
)

const (
	VERSION                   = "0.0.1"
	defaultPlaceholder        = "{{}}"
	keyBackspace              = 8
	keyDelete                 = 127
	keyEndOfTransmission      = 4
	keyLineFeed               = 10
	keyCarriageReturn         = 13
	keyEndOfTransmissionBlock = 23
	keyEscape                 = 27
)

var placeholder string

var usage = `fzz allows you to run a command interactively.

Usage:

	fzz command

The command MUST include the placeholder '{{}}'.

Arguments:

	-v		Print version and exit
`

func printUsage() {
	fmt.Printf(usage)
}

func isPipe(f *os.File) bool {
	s, err := f.Stat()
	if err != nil {
		return false
	}

	return s.Mode()&os.ModeNamedPipe != 0
}

func containsPlaceholder(s []string, ph string) bool {
	for _, v := range s {
		if strings.Contains(v, ph) {
			return true
		}
	}
	return false
}

func validPlaceholder(p string) bool {
	return len(p)%2 == 0
}

func removeLastWord(s []byte) []byte {
	fields := bytes.Fields(s)
	if len(fields) > 0 {
		r := bytes.Join(fields[:len(fields)-1], []byte{' '})
		if len(r) > 1 {
			r = append(r, ' ')
		}
		return r
	}
	return []byte{}
}

func removeLastCharacter(s []byte) []byte {
	if len(s) > 1 {
		r, rsize := utf8.DecodeLastRune(s)
		if r == utf8.RuneError {
			return s[:len(s)-1]
		} else {
			return s[:len(s)-rsize]
		}
	} else if len(s) == 1 {
		return nil
	}
	return s
}

func readCharacter(r io.Reader) <-chan []byte {
	ch := make(chan []byte)
	rs := bufio.NewScanner(r)

	go func() {
		rs.Split(bufio.ScanRunes)

		for rs.Scan() {
			b := rs.Bytes()
			ch <- b
		}
	}()

	return ch
}

type Fzz struct {
	tty           *TTY
	printer       *Printer
	stdinbuf      *bytes.Buffer
	currentRunner *Runner
	input         []byte
}

func (fzz *Fzz) Loop() {
	ttych := readCharacter(fzz.tty)

	fzz.tty.resetScreen()
	fzz.tty.printPrompt(fzz.input[:len(fzz.input)])

	for {
		b := <-ttych

		switch b[0] {
		case keyBackspace, keyDelete:
			fzz.input = removeLastCharacter(fzz.input)
		case keyEndOfTransmission, keyLineFeed, keyCarriageReturn:
			if fzz.currentRunner != nil {
				fzz.currentRunner.Wait()
				fzz.tty.resetScreen()
				io.Copy(os.Stdout, fzz.currentRunner.stdoutbuf)
			} else {
				fzz.tty.resetScreen()
			}
			return
		case keyEscape:
			fzz.tty.resetScreen()
			return
		case keyEndOfTransmissionBlock:
			fzz.input = removeLastWord(fzz.input)
		default:
			// TODO: Default is wrong here. Only append printable characters to
			// input
			fzz.input = append(fzz.input, b...)
		}

		fzz.killCurrentRunner()

		fzz.tty.resetScreen()
		fzz.tty.printPrompt(fzz.input[:len(fzz.input)])

		fzz.printer.Reset()

		if len(fzz.input) > 0 {
			fzz.currentRunner = NewRunner(flag.Args(), placeholder, string(fzz.input), fzz.stdinbuf)
			ch, err := fzz.currentRunner.Run()
			if err != nil {
				log.Fatal(err)
			}

			go func(inputlen int) {
				for line := range ch {
					fzz.printer.Print(line)
				}
				fzz.tty.cursorAfterPrompt(inputlen)
			}(utf8.RuneCount(fzz.input))
		}
	}
}

func (fzz *Fzz) killCurrentRunner() {
	if fzz.currentRunner != nil {
		go func(runner *Runner) {
			runner.KillWait()
		}(fzz.currentRunner)
	}
}

func main() {
	flVersion := flag.Bool("v", false, "Print fzz version and quit")
	flag.Usage = printUsage
	flag.Parse()

	if *flVersion {
		fmt.Printf("fzz %s\n", VERSION)
		os.Exit(0)
	}

	if len(flag.Args()) < 2 {
		fmt.Fprintf(os.Stderr, usage)
		os.Exit(1)
	}

	if placeholder = os.Getenv("FZZ_PLACEHOLDER"); placeholder == "" {
		placeholder = defaultPlaceholder
	}

	if !validPlaceholder(placeholder) {
		fmt.Fprintln(os.Stderr, "Placeholder is not valid, needs even number of characters")
		os.Exit(1)
	}

	if !containsPlaceholder(flag.Args(), placeholder) {
		fmt.Fprintln(os.Stderr, "No placeholder in arguments")
		os.Exit(1)
	}

	tty, err := NewTTY()
	if err != nil {
		log.Fatal(err)
	}
	defer tty.resetState()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		tty.resetState()
		os.Exit(1)
	}()
	tty.setSttyState("cbreak", "-echo")

	stdinbuf := bytes.Buffer{}
	if isPipe(os.Stdin) {
		io.Copy(&stdinbuf, os.Stdin)
	}

	printer := NewPrinter(tty, tty.cols, tty.rows-1) // prompt is one row
	fzz := &Fzz{
		printer: printer,
		tty: tty,
		stdinbuf: &stdinbuf,
		input: make([]byte, 0),
	}
	fzz.Loop()
}
