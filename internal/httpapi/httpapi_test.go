package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "rating", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

// part describes a multipart form field.
type part struct {
	name, value string
	file        bool
}

func report(xml string) part        { return part{name: "report", value: xml, file: true} }
func field(name, value string) part { return part{name: name, value: value} }

func form(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		var w io.Writer
		var err error
		if p.file {
			w, err = mw.CreateFormFile(p.name, "report.xml")
		} else {
			w, err = mw.CreateFormField(p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, p.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func post(t *testing.T, h http.Handler, parts ...part) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := form(t, parts...)
	req := httptest.NewRequest(http.MethodPost, "/rating", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body)
	}
	return out
}

// The HTTP response must match the golden rating payload.
func TestRateReturnsTheAppsPayload(t *testing.T) {
	rec := post(t, New(Options{}),
		report(fixture(t, "report3.xml")),
		field("monthly_income", "24000"),
		field("as_of", "2026-07-08"),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var want map[string]any
	golden, err := os.ReadFile(filepath.Join("..", "..", "rating", "testdata", "golden", "report3_income_24000.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if got := decode(t, rec); !reflect.DeepEqual(got, want) {
		t.Errorf("payload differs from rating/testdata/golden/report3_income_24000.json:\n%s", rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":  "application/json; charset=utf-8",
		"Cache-Control": "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// Omitted or empty optional fields use the report date and the normal income fallback.
func TestOptionalFieldsMayBeAbsentOrEmpty(t *testing.T) {
	for _, parts := range [][]part{
		{report(fixture(t, "report3.xml"))},
		{report(fixture(t, "report3.xml")), field("monthly_income", ""), field("as_of", " ")},
	} {
		rec := post(t, New(Options{}), parts...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
		}
		got := decode(t, rec)
		if got["as_of"] != "2026-07-08" || got["score"] != float64(81) {
			t.Errorf("as_of = %v, score = %v; want the build date 2026-07-08 and 81", got["as_of"], got["score"])
		}
		if _, ok := got["income_source"]; ok {
			t.Errorf("income_source = %v with no income supplied", got["income_source"])
		}
	}
}

func TestRejectsBadRequests(t *testing.T) {
	valid := report(fixture(t, "report3.xml"))
	tests := []struct {
		name   string
		parts  []part
		status int
		code   string
	}{
		{"no report part", []part{field("monthly_income", "25000")}, 400, "missing_report"},
		{"an empty report", []part{report("")}, 400, "missing_report"},
		{"broken XML", []part{report("<ubkidata><comp")}, 400, "invalid_report"},
		{"not a УБКІ document", []part{report("<report/>")}, 400, "invalid_report"},
		{"an unknown part", []part{valid, field("income", "25000")}, 400, "invalid_request"},
		{"a repeated part", []part{valid, field("as_of", "2026-07-08"), field("as_of", "2026-07-09")}, 400, "invalid_request"},
		{"an income that is not a number", []part{valid, field("monthly_income", "lots")}, 400, "invalid_request"},
		{"a negative income", []part{valid, field("monthly_income", "-5")}, 400, "invalid_request"},
		{"an infinite income", []part{valid, field("monthly_income", "Inf")}, 400, "invalid_request"},
		{"a NaN income", []part{valid, field("monthly_income", "NaN")}, 400, "invalid_request"},
		{"a date in the wrong form", []part{valid, field("as_of", "08.07.2026")}, 400, "invalid_request"},
		// report3 was built on 2026-07-08; earlier reference dates are invalid.
		{"a date before the report was built", []part{valid, field("as_of", "2026-07-07")}, 400, "invalid_request"},
		{"an overlong field", []part{valid, field("as_of", strings.Repeat("2", 65))}, 400, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, New(Options{}), tt.parts...)
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.status, rec.Body)
			}
			if got := decode(t, rec); got["error"] != tt.code || got["message"] == "" {
				t.Errorf("error = %v (%v), want %q with a message", got["error"], got["message"], tt.code)
			}
		})
	}
}

func TestRejectsBodiesThatAreNotMultipart(t *testing.T) {
	for _, contentType := range []string{"", "application/xml", "application/json"} {
		req := httptest.NewRequest(http.MethodPost, "/rating", strings.NewReader(fixture(t, "report3.xml")))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		New(Options{}).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnsupportedMediaType || decode(t, rec)["error"] != "unsupported_media_type" {
			t.Errorf("Content-Type %q: status %d (body: %s), want 415", contentType, rec.Code, rec.Body)
		}
	}
}

func TestMalformedMultipartIsABadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/rating", strings.NewReader("--other\r\n\r\nnot a part"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=expected")
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || decode(t, rec)["error"] != "invalid_request" {
		t.Errorf("status %d (body: %s), want 400 invalid_request", rec.Code, rec.Body)
	}
}

func TestOversizedBodyIs413(t *testing.T) {
	rec := post(t, New(Options{}), report(strings.Repeat("x", maxRequestBytes)))

	if rec.Code != http.StatusRequestEntityTooLarge || decode(t, rec)["error"] != "report_too_large" {
		t.Errorf("status %d (body: %s), want 413 report_too_large", rec.Code, rec.Body)
	}
}

// failingReader simulates a connection failure during upload.
type failingReader struct{ r io.Reader }

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errors.New("connection reset by peer")
	}
	return n, err
}

// An interrupted upload should return 400, not a size-limit error.
func TestReadFailureIsNotReportedAsTooLarge(t *testing.T) {
	body, contentType := form(t, report(fixture(t, "report3.xml")))
	truncated := bytes.NewReader(body.Bytes()[:body.Len()/2])
	req := httptest.NewRequest(http.MethodPost, "/rating", &failingReader{truncated})
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || decode(t, rec)["error"] != "invalid_request" {
		t.Errorf("status %d (body: %s), want 400 invalid_request", rec.Code, rec.Body)
	}
}

// XML parse errors must not expose report contents in the response.
func TestParseFailureDoesNotEchoTheReport(t *testing.T) {
	rec := post(t, New(Options{}), report(`<ubkidata><cki inn="1234567890" lname="Петренко"`))

	if body := rec.Body.String(); strings.Contains(body, "1234567890") || strings.Contains(body, "Петренко") {
		t.Errorf("the error response echoed the report: %s", body)
	}
}

// NaN amounts in the report must not prevent JSON encoding.
func TestNonFiniteAmountsStillEncode(t *testing.T) {
	poisoned := strings.Replace(fixture(t, "report3.xml"), `dlamtpaym="`, `dlamtpaym="NaN`, 1)
	rec := post(t, New(Options{}), report(poisoned))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	decode(t, rec)
}

func TestOnlyPostRates(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rating", nil))

	if rec.Code != http.StatusMethodNotAllowed || decode(t, rec)["error"] != "method_not_allowed" {
		t.Errorf("status %d (body: %s), want 405 method_not_allowed", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Allow"); got != "POST, OPTIONS" {
		t.Errorf("Allow = %q", got)
	}
}

func TestCORS(t *testing.T) {
	const origin = "https://app.example"

	preflight := httptest.NewRecorder()
	New(Options{CORSOrigin: origin}).ServeHTTP(preflight, httptest.NewRequest(http.MethodOptions, "/rating", nil))
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("preflight: status %d, origin %q; want 204 and %q",
			preflight.Code, preflight.Header().Get("Access-Control-Allow-Origin"), origin)
	}

	if got := post(t, New(Options{CORSOrigin: origin}), report(fixture(t, "report3.xml"))).Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("POST with CORS on: Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := post(t, New(Options{}), report(fixture(t, "report3.xml"))).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("POST with CORS off: Access-Control-Allow-Origin = %q, want none", got)
	}
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// All responses, including errors, must disable caching.
func TestNoResponseIsCacheable(t *testing.T) {
	h := New(Options{})
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a rating":          post(t, h, report(fixture(t, "report3.xml"))),
		"a rejected report": post(t, h, report("<ubkidata><comp")),
		"a wrong method":    do(h, httptest.NewRequest(http.MethodGet, "/rating", nil)),
		"health":            do(h, httptest.NewRequest(http.MethodGet, "/health", nil)),
	} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "ok" {
		t.Errorf("status %d (body: %s), want 200 {\"status\":\"ok\"}", rec.Code, rec.Body)
	}
}
