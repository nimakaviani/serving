package queue

import (
	"net/http"
)

type Status int

const (
	InProgress Status = iota
	Ready
)

type AsyncCallRecord struct {
	Guid   string
	Resp   *ResponseCache
	Status Status
}

type ResponseCache struct {
	Body       []byte
	StatusCode int
}

func (r *ResponseCache) Write(body []byte) (int, error) {
	r.Body = body
	return len(r.Body), nil
}

func (r *ResponseCache) WriteHeader(code int) {
	r.StatusCode = code
}

func (r *ResponseCache) Header() http.Header {
	return map[string][]string{}
}

func (r *ResponseCache) CloseNotify() <-chan bool {
	return make(chan bool)
}
