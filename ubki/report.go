package ubki

import (
	"encoding/xml"
	"fmt"
	"time"
)

// УБКІ component IDs used by the rating engine.
const (
	SectionIdentity    = 1  // identity and declared employment
	SectionDeals       = 2  // credit deals
	SectionEnforcement = 3  // court enforcement proceedings
	SectionInquiries   = 4  // who requested the subject's history, and why
	SectionMonitoring  = 6  // creditors subscribed to updates about the subject
	SectionSummary     = 78 // the bureau's own aggregates
)

// Report represents the <ubkidata> document.
type Report struct {
	XMLName  xml.Name    `xml:"ubkidata"`
	Trace    []TraceStep `xml:"tech>trace>step"`
	Sections []Section   `xml:"comp"`
}

// TraceStep records a step in report generation.
type TraceStep struct {
	Name     string `xml:"name,attr"`
	Finished string `xml:"ftm,attr"` // 2006-01-02 15:04:05.000
}

// Section represents a <comp> element. All supported child types are declared here
// because encoding/xml cannot select a struct by the id attribute.
type Section struct {
	ID          int            `xml:"id,attr"`
	Subject     *Subject       `xml:"cki"`            // SectionIdentity
	Deals       []Deal         `xml:"crdeal"`         // SectionDeals
	Enforcement *Enforcement   `xml:"penaltiesCount"` // SectionEnforcement
	Inquiries   []Inquiry      `xml:"credres"`        // SectionInquiries
	Monitoring  []Subscription `xml:"moncredres"`     // SectionMonitoring
	Summary     *Summary       `xml:"creditSummary"`  // SectionSummary
}

// Subject holds the declared employment used for the income fallback.
type Subject struct {
	Employment []Employment `xml:"work"`
}

// Employment contains self-reported job and income data.
type Employment struct {
	MonthlyIncome Money `xml:"wdohod,attr"`
	VerifiedOn    Date  `xml:"vdate,attr"`
}

// Deal represents a credit agreement and its monthly history.
type Deal struct {
	Role       Role       `xml:"dlrolesub,attr"`
	LenderType string     `xml:"dldonor,attr"`   // CreditorBank, CreditorMFO, CreditorFinance
	Purpose    string     `xml:"dlcelcred,attr"` // PurposeCreditCard, …
	History    []Snapshot `xml:"deallife"`
}

// Snapshot contains the bureau's monthly record of a deal.
type Snapshot struct {
	ReportedOn  Date       `xml:"dldateclc,attr"`
	Status      DealStatus `xml:"dlflstat,attr"`
	StartedOn   Date       `xml:"dlds,attr"`
	DaysOverdue Int        `xml:"dldayexp,attr"`
	Overdue     Money      `xml:"dlamtexp,attr"`  // amount past due
	Balance     Money      `xml:"dlamtcur,attr"`  // outstanding balance
	Payment     Money      `xml:"dlamtpaym,attr"` // scheduled monthly payment
	Limit       Money      `xml:"dlamtlim,attr"`  // credit limit
}

// Enforcement contains the count of open enforcement proceedings.
type Enforcement struct {
	Active Int `xml:"activeCount,attr"`
}

// Inquiry records a request for the subject's credit history.
type Inquiry struct {
	Requester  string `xml:"org,attr"`        // CreditorBank, …, CreditorOwn
	Reason     string `xml:"reqreason,attr"`  // ReasonCredit, ReasonOnlineCredit, …
	ReportType string `xml:"typereport,attr"` // ReportTypeIdentity, …
	Date       Date   `xml:"redate,attr"`
}

// Subscription records a creditor's request for credit-history updates. These records do
// not affect the rating.
type Subscription struct {
	Subscriber string `xml:"org,attr"`
	Start      Date   `xml:"startdate,attr"`
	End        Date   `xml:"enddate,attr"`
}

// Summary contains the bureau's aggregate credit figures.
type Summary struct {
	MonthlyObligations Money `xml:"totalOblPay,attr"`
}

// Parse decodes УБКІ XML. Invalid attributes become zero or unknown; malformed XML
// returns an error.
func Parse(data []byte) (*Report, error) {
	var r Report
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("ubki: parse report: %w", err)
	}
	return &r, nil
}

// Deals returns all credit deals, including guarantees.
func (r *Report) Deals() []Deal {
	if s := r.section(SectionDeals); s != nil {
		return s.Deals
	}
	return nil
}

// Inquiries returns the inquiry registry. The boolean is false when the section is
// missing, rather than present but empty.
func (r *Report) Inquiries() ([]Inquiry, bool) {
	s := r.section(SectionInquiries)
	if s == nil {
		return nil, false
	}
	return s.Inquiries, true
}

// Monitoring returns the update subscriptions.
func (r *Report) Monitoring() []Subscription {
	if s := r.section(SectionMonitoring); s != nil {
		return s.Monitoring
	}
	return nil
}

// ActiveEnforcements returns the open enforcement count.
func (r *Report) ActiveEnforcements() int {
	if s := r.section(SectionEnforcement); s != nil && s.Enforcement != nil {
		return int(s.Enforcement.Active)
	}
	return 0
}

// DeclaredEmployment returns the self-reported employment records.
func (r *Report) DeclaredEmployment() []Employment {
	if s := r.section(SectionIdentity); s != nil && s.Subject != nil {
		return s.Subject.Employment
	}
	return nil
}

// MonthlyObligations returns the bureau's monthly payment total, or zero if unavailable.
func (r *Report) MonthlyObligations() Money {
	if s := r.section(SectionSummary); s != nil && s.Summary != nil {
		return s.Summary.MonthlyObligations
	}
	return 0
}

// buildStep identifies the trace step used for the report build timestamp.
const buildStep = "build report"

// traceLayout is the trace timestamp format. time.Parse also accepts fractional seconds.
const traceLayout = "2006-01-02 15:04:05"

// BuiltAt uses the build step timestamp, then the first readable trace timestamp, then
// the OWN inquiry date. The boolean is false if none is available.
func (r *Report) BuiltAt() (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	if t, ok := stepFinished(r.Trace, buildStep); ok {
		return t, true
	}
	if t, ok := stepFinished(r.Trace, ""); ok {
		return t, true
	}
	inquiries, _ := r.Inquiries()
	for _, q := range inquiries {
		if t, ok := q.Date.Time(); ok && q.Requester == CreditorOwn {
			return t, true
		}
	}
	return time.Time{}, false
}

// stepFinished returns the first readable timestamp for name, or for any step when name
// is empty.
func stepFinished(steps []TraceStep, name string) (time.Time, bool) {
	for _, s := range steps {
		if name != "" && s.Name != name {
			continue
		}
		if t, err := time.Parse(traceLayout, s.Finished); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (r *Report) section(id int) *Section {
	if r == nil {
		return nil
	}
	for i := range r.Sections {
		if r.Sections[i].ID == id {
			return &r.Sections[i]
		}
	}
	return nil
}

// Latest returns the most recent snapshot, or a zero Snapshot if the history is empty.
// Earlier dated entries are skipped; equal dates and undated entries use file order.
func (d Deal) Latest() Snapshot {
	var latest Snapshot
	var newest time.Time
	for _, s := range d.History {
		at, dated := s.ReportedOn.Time()
		if dated && at.Before(newest) {
			continue
		}
		latest = s
		if dated {
			newest = at
		}
	}
	return latest
}
