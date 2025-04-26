package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

type ResponseWriterWrapper struct {
	conn       net.Conn
	headers    http.Header
	buff       bytes.Buffer
	statusCode int
}

func SendHttpResponse(conn net.Conn, statusCode int, msg string, args ...any) {
	msgStruct := struct {
		Message string `json:"message"`
	}{
		Message: fmt.Sprintf(msg, args...),
	}

	data, err := json.Marshal(msgStruct)
	if err != nil {
		logrus.Warnf("failed to marshal response: %s", err)
		return
	}

	buf := bytes.NewReader(data)

	res := http.Response{
		ProtoMajor: 1,
		ProtoMinor: 0,
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(buf),
		ContentLength: int64(buf.Len()),
	}

	err = res.Write(conn)
	if err != nil {
		logrus.Warnf("failed to write response: %s", err)
		return
	}
}

func NewResponseWriterWrapper(conn net.Conn) *ResponseWriterWrapper {
	return &ResponseWriterWrapper{
		conn:    conn,
		headers: http.Header{},
		buff:    bytes.Buffer{},
	}
}

func (rw *ResponseWriterWrapper) Header() http.Header {
	return rw.headers
}
func (rw *ResponseWriterWrapper) Write(data []byte) (int, error) {
	return rw.buff.Write(data)
}
func (rw *ResponseWriterWrapper) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
}

func (rw *ResponseWriterWrapper) Flush() {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}

	if rw.headers.Get("Content-Length") == "" {
		rw.headers.Set("Content-Length", strconv.Itoa(rw.buff.Len()))
	}

	rw.conn.Write(
		fmt.Appendf(nil, "HTTP/1.1 %d %s\r\n", rw.statusCode, http.StatusText(rw.statusCode)),
	)
	for k, v := range rw.headers {
		rw.conn.Write(fmt.Appendf(nil, "%s: %s\r\n", k, strings.Join(v, ",")))
	}
	rw.conn.Write([]byte("\r\n"))
	rw.conn.Write(rw.buff.Bytes())
}
