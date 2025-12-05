package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// Ensures gofmt doesn't remove the "net" and "os" imports above (feel free to remove this!)
var _ = net.Listen
var _ = os.Exit

const (
	CRLF string = "\r\n"
)

type Server struct {
	ctx             context.Context
	listener        net.Listener
	directory       string
	activeRequests  []http.Request
	errCh           chan error
	cancelCauseFunc context.CancelCauseFunc
}

func main() {
	server := createNewServer()

	directory := flag.String("directory", "", "directory path for files endpoint")
	flag.Parse()

	server.directory = *directory
	server.run()

	// wait for cancel channel of the server to cancel connection

}

func createNewServer() *Server {
	ctx, cancelcause := context.WithCancelCause(context.Background())

	listener, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		os.Exit(1)
	}

	return &Server{
		ctx:             ctx,
		listener:        listener,
		activeRequests:  make([]http.Request, 0),
		errCh:           make(chan error),
		cancelCauseFunc: cancelcause,
	}
}

func (server Server) run() {
	go func() error {
		// wait for connections, handle one at a time
		for {
			conn, err := server.listener.Accept()
			if err != nil {
				server.errCh <- err
			}
			go server.handleConnection(conn)
		}
	}()

	<-server.errCh
}

func (server Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// inside same connection read many times
	for {
		// parse the request
		request, err := http.ReadRequest(reader)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			fmt.Println("maybe serious error: ", err)
			server.errCh <- err
			break
		}

		//fmt.Println("req: ", request)
		// parse the request path
		path, rest := returnFirstSegmentOfThePath(request.URL.Path)
		switch path {
		case "":
			conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
		case "echo":
			server.handleEcho(conn, request, rest)
		case "user-agent":
			server.handleUserAgent(conn, request)
		case "files":
			switch request.Method {
			case http.MethodGet:
				server.handleFilesGet(conn, request, rest)
			case http.MethodPost:
				server.handleFilesPost(conn, request, rest)
			}

		default:
			return404(conn)
		}

		// if connection is supposed to be closed break and clsoe the connection
		if request.Header.Get("Connection") == "close" {
			break
		}
	}

}

func (server Server) handleEcho(conn net.Conn, req *http.Request, restStr string) {
	respondSuccessWithBody(conn, req, restStr, "text/plain", req.Header.Get("Accept-Encoding"))
}

func (server Server) handleUserAgent(conn net.Conn, req *http.Request) {
	respondSuccessWithBody(conn, req, req.Header.Get("User-Agent"), "text/plain")

}

func (server Server) handleFilesGet(conn net.Conn, req *http.Request, fileName string) {
	filePath := string(server.directory + "/" + fileName)

	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return404(conn)
		return
	}

	respondSuccessWithBody(conn, req, string(fileBytes), "application/octet-stream")
}

func (server Server) handleFilesPost(conn net.Conn, req *http.Request, fileName string) {
	filePath := string(server.directory + "/" + fileName)

	file, err := os.Create(filePath)
	if err != nil {
		return404(conn)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return404(conn)
		return
	}

	_, err = file.Write(body)
	if err != nil {
		return404(conn)
		return
	}

	respondSuccess201(conn, "application/octet-stream")
}

func return404(conn net.Conn) {
	conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))
}

func respondSuccessWithBody(conn net.Conn, req *http.Request, respBody string, contentType string, contentEncoding ...string) {
	resp := http.Response{
		Status:     http.StatusText(http.StatusOK),
		StatusCode: http.StatusOK,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", contentType)

	if len(contentEncoding) > 0 && strings.Contains(contentEncoding[0], "gzip") {
		n, compressedData := compressWithGzip([]byte(respBody))
		if n < 0 {
			return404(conn)
		}
		resp.Body = io.NopCloser(bytes.NewReader(compressedData))
		resp.ContentLength = int64(len(compressedData))
		resp.Header.Set("Content-Encoding", "gzip")
	} else {
		resp.Body = io.NopCloser(strings.NewReader(respBody))
		resp.ContentLength = int64(len(respBody))
	}

	if req.Header.Get("Connection") == "close" {
		resp.Header.Set("Connection", "close")
	}

	resp.Write(conn)
}

func respondSuccess201(conn net.Conn, contentType string) {

	resp := http.Response{
		Status:     http.StatusText(http.StatusCreated),
		StatusCode: http.StatusCreated,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", contentType)
	resp.Write(conn)
}

func returnFirstSegmentOfThePath(path string) (string, string) {
	// remove the slashes in the beginning and end with Trim.
	path = strings.Trim(path, "/")
	if len(path) == 0 {
		return "", ""
	}

	// get the first part of the remaining path, divided with /
	pathAndRest := strings.SplitN(path, "/", 2)
	path = pathAndRest[0]
	rest := ""
	if len(pathAndRest) > 1 {
		rest = pathAndRest[1]
	}

	return path, rest
}

func compressWithGzip(data []byte) (int, []byte) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)

	n, err := writer.Write(data)
	if err != nil {
		return -1, nil
	}

	err = writer.Close()
	if err != nil {
		return -1, nil
	}

	return n, buffer.Bytes()
}
