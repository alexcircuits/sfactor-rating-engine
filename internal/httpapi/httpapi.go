// Package httpapi provides POST /rating and GET /health.
//
// POST /rating accepts a multipart body with report, monthly_income and as_of.
// See api/openapi.yaml for the contract. Authentication and rate limiting are handled by
// the gateway.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/sfactor/scoring/rating"
	"github.com/sfactor/scoring/ubki"
)

// maxRequestBytes limits the total multipart request size.
const maxRequestBytes = 4 << 20

// maxFieldBytes limits form fields other than the XML report.
const maxFieldBytes = 64

// Accepted multipart field names.
const (
	fieldReport        = "report"
	fieldMonthlyIncome = "monthly_income"
	fieldAsOf          = "as_of"
)

// Options configures the HTTP handler.
type Options struct {
	// CORSOrigin is the allowed browser origin. Leave it empty to omit CORS headers.
	CORSOrigin string

	// Logger records internal errors, including XML parse failures. A nil logger
	// discards them.
	Logger *slog.Logger
}

// New returns a handler for the rating API.
func New(opts Options) http.Handler {
	s := &server{logger: opts.Logger, cfg: rating.DefaultConfig()}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /rating", s.rate)
	mux.HandleFunc("OPTIONS /rating", preflight)
	mux.HandleFunc("/rating", s.methodNotAllowed)
	mux.HandleFunc("GET /health", s.health)
	return withCORS(opts.CORSOrigin, mux)
}

type server struct {
	logger *slog.Logger
	cfg    rating.Config
}

func (s *server) rate(w http.ResponseWriter, r *http.Request) {
	req, apiErr := readRequest(w, r)
	if apiErr != nil {
		s.writeError(w, apiErr)
		return
	}
	report, err := ubki.Parse(req.report)
	if err != nil {
		// Parse errors may contain report data, so keep the details out of the
		// response.
		s.logger.Warn("report does not parse", "error", err)
		s.writeError(w, &apiError{http.StatusBadRequest, "invalid_report", "The report is not УБКІ XML."})
		return
	}

	in := rating.Input{Report: report, MonthlyIncome: req.monthlyIncome, AsOf: req.asOf}
	if err := in.Validate(); err != nil {
		s.writeError(w, invalidRequest("as_of is earlier than the report's build date: a rating cannot rewind a report."))
		return
	}
	s.writeJSON(w, http.StatusOK, rating.Rate(in, s.cfg))
}

// request holds the validated multipart fields.
type request struct {
	report        []byte
	monthlyIncome ubki.Money
	asOf          time.Time
}

// readRequest parses and validates each multipart field. Error responses exclude report
// contents.
func readRequest(w http.ResponseWriter, r *http.Request) (request, *apiError) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return request{}, &apiError{http.StatusUnsupportedMediaType, "unsupported_media_type",
			`Send multipart/form-data with the report in a part named "report".`}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	parts, err := r.MultipartReader()
	if err != nil {
		return request{}, invalidRequest("The body is not valid multipart/form-data.")
	}

	var req request
	seen := make(map[string]bool)
	for {
		part, err := parts.NextPart()
		// A plain EOF marks the closing boundary. A wrapped EOF means the upload
		// ended early.
		if err == io.EOF {
			break
		}
		if err != nil {
			return request{}, bodyError(err)
		}
		name := part.FormName()
		if seen[name] {
			return request{}, invalidRequest(fmt.Sprintf("The %q part is repeated.", name))
		}
		seen[name] = true
		if err := req.read(name, part); err != nil {
			return request{}, bodyError(err)
		}
	}
	if len(req.report) == 0 {
		return request{}, &apiError{http.StatusBadRequest, "missing_report",
			`Attach the УБКІ XML report in a part named "report".`}
	}
	return req, nil
}

// read validates one multipart part. Empty optional fields are treated as omitted.
func (req *request) read(name string, part io.Reader) error {
	switch name {
	case fieldReport:
		report, err := io.ReadAll(part)
		req.report = report
		return err

	case fieldMonthlyIncome:
		raw, err := readField(part)
		if err != nil || raw == "" {
			return err
		}
		income, err := ubki.ParseMoney(raw)
		if err != nil || income < 0 {
			return invalidRequest("monthly_income must be a non-negative amount of hryvnias, such as 25000 or 25000.50.")
		}
		req.monthlyIncome = income

	case fieldAsOf:
		raw, err := readField(part)
		if err != nil || raw == "" {
			return err
		}
		asOf, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			return invalidRequest("as_of must be a date in YYYY-MM-DD form.")
		}
		req.asOf = asOf

	default:
		return invalidRequest(fmt.Sprintf("Unknown part %q: send report, and optionally monthly_income and as_of.", name))
	}
	return nil
}

// readField reads and trims a form field within the size limit.
func readField(part io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxFieldBytes {
		return "", invalidRequest(fmt.Sprintf("Form fields other than report are at most %d bytes.", maxFieldBytes))
	}
	return strings.TrimSpace(string(b)), nil
}

// apiError holds the status and message returned to the caller.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func invalidRequest(message string) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_request", message}
}

// bodyError maps size-limit failures to 413 and other read failures to 400.
func bodyError(err error) *apiError {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &apiError{http.StatusRequestEntityTooLarge, "report_too_large", "The request is larger than 4 MiB."}
	}
	return invalidRequest("The body could not be read as multipart/form-data.")
}

func (s *server) methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "POST, OPTIONS")
	s.writeError(w, &apiError{http.StatusMethodNotAllowed, "method_not_allowed", "Use POST with a multipart/form-data body."})
}

// preflight handles OPTIONS requests. withCORS adds the response headers.
func preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// errorBody is the JSON error response.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (s *server) writeError(w http.ResponseWriter, e *apiError) {
	s.writeJSON(w, e.status, errorBody{Error: e.code, Message: e.message})
}

// writeJSON disables caching for every response, including errors about a report.
func (s *server) writeJSON(w http.ResponseWriter, status int, body any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("write response", "error", err)
	}
}

// withCORS adds headers for the configured origin, if any.
func withCORS(origin string, next http.Handler) http.Handler {
	if origin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		next.ServeHTTP(w, r)
	})
}
