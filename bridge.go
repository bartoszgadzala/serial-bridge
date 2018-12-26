package main

import (
	"encoding/hex"
	"flag"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tarm/serial"
)

type worker struct {
	source chan []byte
	conn   *net.Conn
}

type threadSafeSlice struct {
	sync.Mutex
	workers []*worker
}

func (s *threadSafeSlice) Push(w *worker) {
	s.Lock()
	defer s.Unlock()

	s.workers = append(s.workers, w)
}

func (s *threadSafeSlice) Iter(routine func(*worker)) {
	s.Lock()
	defer s.Unlock()

	for _, worker := range s.workers {
		routine(worker)
	}
}

func broadcaster(bcast *threadSafeSlice, ch chan []byte) {
	for {
		msg := <-ch
		bcast.Iter(func(w *worker) { w.source <- msg })
	}
}

func configureFormatter() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.DebugLevel)
}

func openSerialPort(port string) io.ReadWriteCloser {
	log.WithFields(log.Fields{
		"port": port,
	}).Debug("Opening serial port")

	c := &serial.Config{Name: port, Baud: 57600}
	s, err := serial.OpenPort(c)
	if err != nil {
		log.Fatal(err)
	}
	return s
}

func readAnswer(in io.Reader, buf []byte) int {
	n, err := in.Read(buf)
	if err != nil {
		log.Fatal(err)
	} else {
		log.WithFields(log.Fields{
			"bytes": n,
			"data":  hex.EncodeToString(buf[:n]),
		}).Debug("Received data")
	}

	return n
}

func openSocket(port int) net.Listener {
	log.WithFields(log.Fields{
		"port": port,
	}).Info("Listening")

	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatal(err)
	}

	return l
}

func handleConnection(w *worker) {
	c := *w.conn
	defer c.Close()

	log.WithFields(log.Fields{
		"remote": c.RemoteAddr(),
	}).Info("Serving new connection")

	for {
		msg := <-w.source
		if _, err := c.Write([]byte(msg)); err == io.EOF {
			log.WithFields(log.Fields{
				"remote": c.RemoteAddr(),
			}).Error("Closing connection")
			break
		}
	}
}

func readFromSerialPort(in io.Reader, ch chan []byte) {
	buf := make([]byte, 1024)
	for {
		n := readAnswer(in, buf)

		data := buf[:n]
		log.WithFields(log.Fields{"data": hex.EncodeToString(data)}).Info("Read from serial")

		ch <- []byte(data)
	}
}

func produceFakeData(ch chan []byte) {
	for {
		data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
		log.WithFields(log.Fields{"data": hex.EncodeToString(data)}).Info("Created fake data")

		ch <- []byte(data)

		time.Sleep(5 * time.Second)
	}
}

func main() {
	configureFormatter()

	port := flag.Int("port", 8543, "TCP port")
	serialPort := flag.String("serialPort", "", "Serial device")
	fakeData := flag.Bool("fake", false, "Fake data")
	flag.Parse()

	if !*fakeData && *serialPort == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	l := openSocket(*port)
	defer l.Close()

	bcast := &threadSafeSlice{
		workers: make([]*worker, 0, 1),
	}
	msgs := make(chan []byte)
	go broadcaster(bcast, msgs)

	if *fakeData {
		go produceFakeData(msgs)
	} else {
		s := openSerialPort(*serialPort)
		defer s.Close()

		go readFromSerialPort(s, msgs)
	}

	for {
		c, err := l.Accept()
		if err != nil {
			log.Error(err)
			break
		}

		wk := &worker{
			source: make(chan []byte),
			conn:   &c,
		}
		bcast.Push(wk)

		go handleConnection(wk)
	}

}
