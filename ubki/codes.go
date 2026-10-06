package ubki

// DealStatus is the УБКІ deal status code (dlflstat).
type DealStatus string

const (
	StatusOpen         DealStatus = "1"
	StatusClosed       DealStatus = "2"
	StatusSold         DealStatus = "3" // sold to a collector
	StatusRestructured DealStatus = "4"
	StatusWrittenOff   DealStatus = "13"
)

// Terminal reports whether the deal was sold or written off.
func (s DealStatus) Terminal() bool { return s == StatusSold || s == StatusWrittenOff }

// Role is the subject's role in the deal (dlrolesub).
type Role string

const (
	RoleBorrower  Role = "1"
	RoleGuarantor Role = "2"
)

// PurposeCreditCard is the credit card purpose code (dlcelcred).
const PurposeCreditCard = "31"

// Lender types used by deal, inquiry and monitoring records.
const (
	CreditorBank    = "BNK"
	CreditorMFO     = "MFO" // microfinance
	CreditorFinance = "FIN"
	CreditorBureau  = "BCH"
	CreditorOwn     = "OWN" // report requester
)

// Inquiry reasons that represent credit applications.
const (
	ReasonCredit       = "2"
	ReasonOnlineCredit = "4"
)

// ReportTypeIdentity identifies reports without credit-history data.
const ReportTypeIdentity = "1"
