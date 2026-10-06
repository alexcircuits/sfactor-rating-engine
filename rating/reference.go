package rating

import (
	"slices"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// Reference blocks provide context without changing the score.

// flagsFor collects sale, write-off, enforcement and MFO pressure flags.
func flagsFor(r *ubki.Report, deals []deal, values map[ParamKey]float64, activeMFODeals int, cfg Config) []Flag {
	var sold, writtenOff bool
	for _, d := range deals {
		for _, s := range d.history {
			sold = sold || s.Status == ubki.StatusSold
			writtenOff = writtenOff || s.Status == ubki.StatusWrittenOff
		}
	}

	var flags []Flag
	if writtenOff {
		flags = append(flags, FlagWriteOff)
	}
	if sold {
		flags = append(flags, FlagSold)
	}
	if r.ActiveEnforcements() > 0 {
		flags = append(flags, FlagEnforcement)
	}
	if mfoPressure(values, activeMFODeals, cfg) {
		flags = append(flags, FlagMFOPressure)
	}
	return flags
}

// mfoPressure checks for a bad inquiry level or at least MFOPressureDeals active MFO
// loans. The inquiry check reuses indicator 9's band rule.
func mfoPressure(values map[ParamKey]float64, activeMFODeals int, cfg Config) bool {
	if n, ok := values[ParamInquiries]; ok && levelOf(ParamInquiries, n) == LevelBad {
		return true
	}
	return activeMFODeals >= cfg.MFOPressureDeals
}

// lenderBreakdown groups the same deals and applications used by the indicators by lender
// type.
func lenderBreakdown(deals []deal, applications []ubki.Inquiry) *MFOProfile {
	p := &MFOProfile{
		DealsByDonor:   map[string]int{},
		ActiveByDonor:  map[string]int{},
		InquiriesByOrg: map[string]int{},
		Inquiries6m:    len(applications),
	}
	for _, d := range deals {
		if d.lenderType == "" {
			continue // skip deals without a lender type
		}
		p.DealsByDonor[d.lenderType]++
		if !d.open() {
			continue
		}
		p.ActiveByDonor[d.lenderType]++
		if d.lenderType == ubki.CreditorMFO {
			p.ActiveMFODeals++
		}
	}
	for _, q := range applications {
		if q.Requester != "" {
			p.InquiriesByOrg[q.Requester]++
		}
	}
	return p
}

// subscriberLabels provides display names for known subscriber types. Unknown codes are
// shown unchanged.
var subscriberLabels = map[string]string{
	ubki.CreditorBank:    "Банк",
	ubki.CreditorMFO:     "МФО",
	ubki.CreditorFinance: "Фінкомпанія",
	ubki.CreditorBureau:  "Бюро",
}

// monitoringEntries returns all subscriptions, including expired ones, sorted by start
// date.
func monitoringEntries(subs []ubki.Subscription, asOf time.Time) []MonitoringEntry {
	if len(subs) == 0 {
		return nil
	}
	subs = slices.Clone(subs)
	slices.SortStableFunc(subs, newestFirst)

	entries := make([]MonitoringEntry, 0, len(subs))
	for _, s := range subs {
		label, ok := subscriberLabels[s.Subscriber]
		if !ok {
			label = s.Subscriber
		}
		end, known := s.End.Time()
		entries = append(entries, MonitoringEntry{
			Org:       s.Subscriber,
			OrgLabel:  label,
			StartDate: s.Start.String(),
			EndDate:   s.End.String(),
			Active:    known && !end.Before(asOf),
		})
	}
	return entries
}

// newestFirst sorts by descending start date, with unknown dates last.
func newestFirst(a, b ubki.Subscription) int {
	at, aKnown := a.Start.Time()
	bt, bKnown := b.Start.Time()
	switch {
	case aKnown && bKnown:
		return bt.Compare(at)
	case aKnown:
		return -1
	case bKnown:
		return 1
	default:
		return 0
	}
}
