package rating

// Result is the JSON rating response. The companion app mirrors this contract in
// src/lib/rating.ts.
type Result struct {
	// Available is false when there are no eligible credit deals to rate.
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`

	AsOf      string  `json:"as_of"` // YYYY-MM-DD
	Score     int     `json:"score"`
	Raw       float64 `json:"raw"` // sum of contributions before caps
	Band      Band    `json:"band"`
	BandLabel string  `json:"band_label"`

	Capped    bool      `json:"capped,omitempty"`
	CapReason CapReason `json:"cap_reason,omitempty"`

	// IncomeSource identifies the income used for debt load. It is empty when debt
	// load is unmeasured.
	IncomeSource IncomeSource `json:"income_source,omitempty"`

	// Flags, MFO and Monitoring do not affect the score.
	Flags      []Flag            `json:"flags,omitempty"`
	MFO        *MFOProfile       `json:"mfo,omitempty"`
	Monitoring []MonitoringEntry `json:"monitoring,omitempty"`

	Parameters []Parameter `json:"parameters"`
}

// Reason explains why a rating is unavailable.
type Reason string

const ReasonNoCreditHistory Reason = "no_credit_history"

// CapReason identifies the cap that reduced the score.
type CapReason string

const (
	CapCurrentOverdue   CapReason = "current_overdue"
	CapTerminalDebt     CapReason = "terminal_debt"
	CapTerminalDebtAged CapReason = "terminal_debt_aged"
)

// IncomeSource identifies the source of monthly income.
type IncomeSource string

const (
	IncomeFromProfile IncomeSource = "profile" // verified profile income
	IncomeFromUBKI    IncomeSource = "ubki"    // recent declared income
)

// Flag describes a credit-history fact outside the scored indicators.
type Flag string

const (
	FlagWriteOff    Flag = "write_off"
	FlagSold        Flag = "sold"
	FlagEnforcement Flag = "enforcement"
	FlagMFOPressure Flag = "mfo_pressure"
)

// Parameter contains the measured value, score and display text for one indicator.
type Parameter struct {
	Key   ParamKey `json:"key"`
	Order int      `json:"order"`
	Title string   `json:"title"`

	// Value is the measurement; Display is its formatted text. Ignore both when Level
	// is LevelUnknown.
	Value   float64 `json:"value"`
	Display string  `json:"display"`
	Level   Level   `json:"level"`

	// Points is the curve score and Weight is the configured percentage. Contribution
	// is the weighted share of Raw after excluding unmeasured indicators.
	Points       float64 `json:"points"`
	Weight       int     `json:"weight"`
	Contribution float64 `json:"contribution"`

	Bands  Bands  `json:"bands"`
	Hint   string `json:"hint"`
	Advice string `json:"advice,omitempty"`
}

// Bands contains the display ranges for each level.
type Bands struct {
	Bad    string `json:"bad"`
	Medium string `json:"medium"`
	Good   string `json:"good"`
}

// MFOProfile groups loans and applications by lender type.
type MFOProfile struct {
	DealsByDonor   map[string]int `json:"deals_by_donor"`   // deals by lender type
	ActiveByDonor  map[string]int `json:"active_by_donor"`  // active deals by lender type
	InquiriesByOrg map[string]int `json:"inquiries_by_org"` // recent applications by lender type
	ActiveMFODeals int            `json:"active_mfo_deals"`
	Inquiries6m    int            `json:"inquiries_6m"` // same count as indicator 9
}

// MonitoringEntry describes a creditor's subscription to credit-history updates.
type MonitoringEntry struct {
	Org       string `json:"org"`
	OrgLabel  string `json:"org_label"`
	StartDate string `json:"start_date"` // YYYY-MM-DD, or empty when unknown
	EndDate   string `json:"end_date"`

	// Active indicates whether the subscription covers the reference date. A missing
	// end date is treated as inactive.
	Active bool `json:"active"`
}

// Parameter returns the indicator with the given key.
func (r Result) Parameter(k ParamKey) (Parameter, bool) {
	for _, p := range r.Parameters {
		if p.Key == k {
			return p, true
		}
	}
	return Parameter{}, false
}
