package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net"
	"strings"
	"unicode"

	"gopkg.in/sorcix/irc.v2"
)

var upstreamAddr string

var errNotEnoughParams = errors.New("not enough parameters")

type sanitizeFunc func(*irc.Message) error

var sanitize = map[string]sanitizeFunc{
	irc.NICK: sanitizeFirstArg(sanitizeNick),
	irc.MODE: sanitizeFirstArg(sanitizeNick),
	irc.SERVICE: sanitizeFirstArg(sanitizeNick),
	irc.INVITE: sanitizeFirstArg(sanitizeNick),
	irc.PRIVMSG: sanitizeMessage,
	irc.NOTICE: sanitizeMessage,
}

func sanitizeFirstArg(sanitize func(string) string) sanitizeFunc {
	return func(msg *irc.Message) error {
		if len(msg.Params) == 0 {
			return errNotEnoughParams
		}
		msg.Params[0] = sanitize(msg.Params[0])
		return nil
	}
}

func sanitizeMessage(msg *irc.Message) error {
	if len(msg.Params) < 2 {
		return errNotEnoughParams
	}
	if len(msg.Params[1]) > 512 {
		// TODO: this doesn't comply with the RFC, but it's better than nothing
		msg.Params[1] = msg.Params[1][:512]
	}
	return nil
}

// ( letter / special ) *8( letter / digit / special / "-" )
func sanitizeNick(nick string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		switch r {
		// %x5B-60 / %x7B-7D
		// ; "[", "]", "\", "`", "_", "^", "{", "|", "}"
	case '-', '[', ']', '\\', '`', '_', '^', '{', '|', '}':
			return r
		}
		return '_'
	}, nick)
}

func proxy(dec *irc.Decoder, enc *irc.Encoder) error {
	for {
		msg, err := dec.Decode()
		if err != nil {
			return err
		}

		if msg.Prefix != nil {
			msg.Prefix.User = sanitizeNick(msg.Prefix.User)
		}

		if f, ok := sanitize[msg.Command]; ok {
			if err := f(msg); err != nil {
				return err
			}
		}

		if err := enc.Encode(msg); err != nil {
			return err
		}
	}
}

func serveConn(conn *irc.Conn) error {
	defer conn.Close()

	upstream, err := irc.DialTLS(upstreamAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return err
	}
	defer upstream.Close()

	done := make(chan error, 2)
	go func() {
		done <- proxy(&conn.Decoder, &upstream.Encoder)
	}()
	go func() {
		done <- proxy(&upstream.Decoder, &conn.Encoder)
	}()
	return <-done
}

func main() {
	flag.Parse()
	if upstreamAddr = flag.Arg(0); upstreamAddr == "" {
		log.Fatal("no upstream specified")
	}

	l, err := net.Listen("tcp", ":6667")
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	log.Println("Proxying", upstreamAddr, "on", l.Addr())

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go func() {
			err := serveConn(irc.NewConn(conn))
			if err != nil {
				log.Println(err)
			}
		}()
	}
}
