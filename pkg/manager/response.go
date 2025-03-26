package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/sirupsen/logrus"
)

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
