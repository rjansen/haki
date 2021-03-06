package http

import (
	"context"
	"github.com/rjansen/haki"
	"github.com/rjansen/haki/media/json"
	"github.com/rjansen/l"
	"github.com/satori/go.uuid"
	"net/http"
	"strings"
	"time"
)

//ResponseWriter is a wrapper function to store status and body length of the request
type ResponseWriter interface {
	http.ResponseWriter
	http.Flusher
	// Status returns the status code of the response or 200 if the response has
	// not been written (as this is the default response code in net/http)
	Status() int
	// Written returns whether or not the ResponseWriter has been written.
	Written() bool
	// Size returns the size of the response body.
	Size() int
}

// NewResponseWriter creates a ResponseWriter that wraps an http.ResponseWriter
func NewResponseWriter(w http.ResponseWriter) ResponseWriter {
	return &responseWriter{
		ResponseWriter: w,
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *responseWriter) WriteHeader(s int) {
	w.status = s
	w.ResponseWriter.WriteHeader(s)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.Written() {
		// The status will be 200 if WriteHeader has not been called yet
		w.WriteHeader(http.StatusOK)
	}
	size, err := w.ResponseWriter.Write(b)
	w.size += size
	return size, err
}

func (w *responseWriter) Status() int {
	return w.status
}

func (w *responseWriter) Size() int {
	return w.size
}

func (w *responseWriter) Written() bool {
	return w.status != 0
}

func (w *responseWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if ok {
		if !w.Written() {
			// The status will be 200 if WriteHeader has not been called yet
			w.WriteHeader(http.StatusOK)
		}
		flusher.Flush()
	}
}

//SimpleHTTPHandler is a contract for fast http handlers
type SimpleHTTPHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

//HTTPHandlerFunc is a function to handle fasthttp requrests
type HTTPHandlerFunc func(http.ResponseWriter, *http.Request) error

//HTTPHandlerWrapper is a function to create handler wraps to execute like a chain mechanism between the handlers
type HTTPHandlerWrapper func(HTTPHandlerFunc) HTTPHandlerFunc

//HandleRequest is the contract with HTTPHandler interface
func (h HTTPHandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return h(w, r)
}

//HTTPHandler is a contract for fast http handlers
type HTTPHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request) error
}

func Wrap(h HTTPHandlerFunc, wrappers ...HTTPHandlerWrapper) http.HandlerFunc {
	currentHandler := h
	for _, w := range wrappers {
		currentHandler = w(currentHandler)
	}
	return Handler(currentHandler)
}

//Handler wraps a library handler func nto a http handler func
func Handler(handler HTTPHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, h *http.Request) {
		handler(w, h)
	}
}

func errorHandle(handler HTTPHandlerFunc, w http.ResponseWriter, r *http.Request) error {
	if err := handler(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	return nil
}

//Error wraps the provided HTTPHandlerFunc with exception control
func Error(handler HTTPHandlerFunc) HTTPHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		return errorHandle(handler, w, r)
	}
}

type ErrorHandler func(http.ResponseWriter, *http.Request) error

func (h ErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errorHandle(HTTPHandlerFunc(h), w, r)
}

func logHandle(handler HTTPHandlerFunc, w http.ResponseWriter, r *http.Request) error {
	tid := uuid.NewV4().String()
	r = r.WithContext(context.WithValue(r.Context(), "tid", tid))
	logger := l.WithFields(
		l.String("tid", tid),
		l.String("method", r.Method),
		l.String("path", r.URL.Path),
	)
	start := time.Now()
	logger.Info("haki.hhtp.Request",
		l.Bool("ctxIsNil", r.Context() == nil),
	)

	r = set(r, ContextKeys.LOG, logger)
	rw := NewResponseWriter(w)
	var err error
	if err = handler(rw, r); err != nil {
		logger.Error("haki.http.RequestErr",
			l.Err(err),
		)
	}
	response := rw.(ResponseWriter)
	logger.Info("haki.http.Response",
		l.String("status", http.StatusText(response.Status())),
		l.Int("size", response.Size()),
		l.Duration("requestTime", time.Since(start)),
	)
	return err
}

//Log wraps the provided HTTPHandlerFunc with access logging control
func Log(handler HTTPHandlerFunc) HTTPHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		return logHandle(handler, w, r)
	}
}

type LogHandler func(http.ResponseWriter, *http.Request) error

func (h LogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logHandle(HTTPHandlerFunc(h), w, r)
}

func auditHandle(handler HTTPHandlerFunc, w http.ResponseWriter, r *http.Request) error {
	start := time.Now()
	tid := uuid.NewV4().String()

	cid := r.Header.Get(haki.RequestContextHeader)

	w.Header().Set(haki.RequestIDHeader, tid)
	w.Header().Set(haki.RequestContextHeader, cid)

	r = set(r, ContextKeys.TID, tid)
	r = set(r, ContextKeys.CID, tid)

	identity := &Identity{
		Token: "tanonymous",
		Value: map[string]interface{}{
			"ID":   "uanonymous",
			"Name": "User Anonymous",
		},
	}
	logger := l.WithFields(
		l.String("tid", tid),
		l.String("cid", cid),
		l.String("method", r.Method),
		l.String("path", r.URL.Path),
		l.String("token", identity.Token),
	)
	auditor := &Auditor{
		TID:      tid,
		CID:      cid,
		Logger:   logger,
		Identity: identity,
	}
	auditor.Info("haki.http.Request",
		l.Bool("ctxIsNil", r.Context() == nil),
	)

	r = set(r, ContextKeys.LOG, logger)
	r = set(r, ContextKeys.TOKEN, identity.Token)
	r = set(r, ContextKeys.IDENTITY, identity)
	r = set(r, ContextKeys.AUDITOR, auditor)

	rw := NewResponseWriter(w)
	var err error
	if err = handler(rw, r); err != nil {
		auditor.Error("haki.http.RequestErr",
			l.Err(err),
		)
	}
	response := rw.(ResponseWriter)
	auditor.Info("haki.http.Response",
		l.String("status", http.StatusText(response.Status())),
		l.Int("size", response.Size()),
		l.Duration("requestTime", time.Since(start)),
	)
	return err
}

//Audit wraps the provided HTTPHandlerFunc with access logging, error and audit control
func Audit(handler HTTPHandlerFunc) HTTPHandlerFunc {
	return Error(
		func(w http.ResponseWriter, r *http.Request) error {
			return auditHandle(handler, w, r)
		},
	)
}

type AuditHandler func(http.ResponseWriter, *http.Request) error

func (h AuditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auditHandle(HTTPHandlerFunc(h), w, r)
}

//ReadByContentType reads data from context using the Content-Type header to define the media type
func ReadByContentType(r *http.Request, data interface{}) error {
	contentType := r.Header.Get(haki.ContentTypeHeader)
	switch {
	case strings.Contains(contentType, json.ContentType):
		return ReadJSON(r, data)
	// case strings.Contains(contentType, proto.ContentType):
	// 	return ReadProtoBuff(r, data)
	default:
		return haki.ErrInvalidContentType
	}
}

//WriteByAccept writes data to context using the Accept header to define the media type
func WriteByAccept(w http.ResponseWriter, r *http.Request, status int, result interface{}) error {
	contentType := r.Header.Get(haki.AcceptHeader)
	switch {
	case strings.Contains(contentType, json.ContentType):
		return JSON(w, status, result)
	// case bytes.Contains(contentType, []byte(proto.ContentType)):
	// 	return ProtoBuff(ctx, status, result)
	default:
		return haki.ErrInvalidAccept
	}
}

//ReadJSON unmarshals from provided context a json media into data
func ReadJSON(r *http.Request, data interface{}) error {
	if err := json.Unmarshal(r.Body, data); err != nil {
		return err
	}
	return nil
}

func Bytes(w http.ResponseWriter, status int, result []byte) error {
	w.Header().Set(haki.ContentTypeHeader, "application/octet-stream")
	w.WriteHeader(status)
	_, err := w.Write(result)
	if err != nil {
		return err
	}
	return nil
}

func JSON(w http.ResponseWriter, status int, result interface{}) error {
	w.Header().Set(haki.ContentTypeHeader, json.ContentType)
	w.WriteHeader(status)
	if err := json.Marshal(w, result); err != nil {
		return err
	}
	return nil
}

func Status(w http.ResponseWriter, status int) error {
	w.WriteHeader(status)
	return nil
}

func Err(w http.ResponseWriter, err error) error {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return err
}

type BaseHandler struct {
}

func (h BaseHandler) JSON(w http.ResponseWriter, status int, result interface{}) error {
	return JSON(w, status, result)
}

func (h BaseHandler) Status(w http.ResponseWriter, status int) error {
	return Status(w, status)
}

func (h BaseHandler) Err(w http.ResponseWriter, err error) error {
	return Err(w, err)
}
