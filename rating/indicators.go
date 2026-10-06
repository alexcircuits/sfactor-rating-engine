package rating

// ParamKey identifies an indicator in the JSON response.
type ParamKey string

const (
	ParamHistoryAge    ParamKey = "history_age"
	ParamCleanStreak   ParamKey = "clean_streak"
	ParamOverdueDeals  ParamKey = "overdue_deals"
	ParamOverdueAmount ParamKey = "overdue_amount"
	ParamActiveDeals   ParamKey = "active_deals"
	ParamClosedDeals   ParamKey = "closed_deals"
	ParamNewDeals      ParamKey = "new_deals_6m"
	ParamDebtLoad      ParamKey = "debt_load"
	ParamInquiries     ParamKey = "inquiries_6m"
)

// indicator defines a metric's bands, curve, weight and display text.
type indicator struct {
	key    ParamKey
	title  string
	hint   string
	bands  Bands                // display labels for the bands
	level  bandRule             // numeric band thresholds
	curve  curve                // maps values to points
	weight int                  // percentage of the total weight
	advice advice               // advice for bad and medium values
	format func(float64) string // display formatter
}

// advice holds the messages for bad and medium levels. Missing medium advice falls back
// to bad.
type advice struct {
	bad, medium string
}

// forLevel returns advice for bad or medium values. Good and unknown values have no
// advice.
func (a advice) forLevel(l Level) string {
	switch l {
	case LevelBad:
		return a.bad
	case LevelMedium:
		if a.medium != "" {
			return a.medium
		}
		return a.bad
	default:
		return ""
	}
}

var credits = countDisplay("кредит", "кредити", "кредитів")

// indicators defines the nine metrics in display order. Thresholds, anchors and weights
// follow the initial model in
// docs/specs/2026-08-07-fhp-9-parameter-rating-design.md.
var indicators = []indicator{
	{
		key:    ParamHistoryAge,
		title:  "Вік кредитної історії",
		hint:   "Що довша історія, то більше довіри з боку кредиторів.",
		bands:  Bands{Bad: "0–180 днів", Medium: "181–360 днів", Good: "більше 360 днів"},
		level:  higherIsBetter(180, 360),
		curve:  curve{{0, 0}, {180, 33}, {360, 66}, {1095, 100}},
		weight: 8,
		advice: advice{
			bad: "Цей показник зростає сам із часом — його не можна прискорити, тільки не переривати.",
		},
		format: daysDisplay,
	},
	{
		key:    ParamCleanStreak,
		title:  "Платежі без прострочень",
		hint:   "Найсильніший сигнал: скільки днів поспіль ви платите вчасно.",
		bands:  Bands{Bad: "0–90 днів", Medium: "91–180 днів", Good: "більше 180 днів"},
		level:  higherIsBetter(90, 180),
		curve:  curve{{0, 0}, {90, 33}, {180, 66}, {730, 100}},
		weight: 18,
		advice: advice{
			bad:    "Кожен вчасний платіж подовжує серію. Три місяці поспіль без прострочень — і показник стане жовтим.",
			medium: "Ви вже близько: після 180 днів поспіль без прострочень показник стане зеленим.",
		},
		format: daysDisplay,
	},
	{
		key:    ParamOverdueDeals,
		title:  "Поточні прострочені кредити",
		hint:   "Кожен прострочений кредит бачить будь-який кредитор.",
		bands:  Bands{Bad: "2 і більше", Medium: "1", Good: "0"},
		level:  lowerIsBetter(1, 1),
		curve:  curve{{0, 100}, {1, 50}, {2, 20}, {3, 8}, {4, 0}},
		weight: 15,
		advice: advice{
			bad: "Погасіть прострочені кредити — це найшвидший спосіб підняти рейтинг.",
		},
		format: credits,
	},
	{
		key:    ParamOverdueAmount,
		title:  "Сума поточного прострочення",
		hint:   "Навіть невелика прострочена сума псує всю картину.",
		bands:  Bands{Bad: "більше 1000 ₴", Medium: "100–1000 ₴", Good: "менше 100 ₴"},
		level:  lowerIsBetter(100, 1000),
		curve:  curve{{0, 100}, {100, 67}, {1000, 34}, {10000, 0}},
		weight: 12,
		advice: advice{
			bad: "Внесіть прострочену суму. Щойно залишок стане меншим за 100 ₴, показник позеленіє.",
		},
		format: uahDisplay,
	},
	{
		key:    ParamActiveDeals,
		title:  "Діючі кредити",
		hint:   "Багато кредитів одночасно — це високе навантаження на бюджет.",
		bands:  Bands{Bad: "більше 5", Medium: "3–5", Good: "менше 3"},
		level:  lowerIsBetter(3, 5),
		curve:  curve{{0, 100}, {2, 70}, {3, 60}, {5, 40}, {6, 28}, {10, 0}},
		weight: 10,
		advice: advice{
			bad:    "Закрийте найменший кредит замість того, щоб брати новий — це знімає навантаження одразу.",
			medium: "Не беріть новий кредит, поки не закриєте хоча б один із діючих.",
		},
		format: credits,
	},
	{
		key:    ParamClosedDeals,
		title:  "Погашені кредити",
		hint:   "Закриті кредити доводять, що ви повертаєте борги.",
		bands:  Bands{Bad: "0", Medium: "1–3", Good: "більше 3"},
		level:  higherIsBetter(0, 3),
		curve:  curve{{0, 0}, {1, 40}, {3, 60}, {4, 75}, {6, 90}, {8, 100}},
		weight: 7,
		advice: advice{
			bad:    "У вас ще немає повністю погашених кредитів. Перший закритий кредит помітно підніме рейтинг.",
			medium: "Кожен повністю погашений кредит додає балів — доведіть поточні до кінця.",
		},
		format: credits,
	},
	{
		key:    ParamNewDeals,
		title:  "Нові кредити за 6 місяців",
		hint:   "Кілька нових кредитів поспіль читаються як фінансовий стрес.",
		bands:  Bands{Bad: "більше 5", Medium: "3–5", Good: "менше 3"},
		level:  lowerIsBetter(3, 5),
		curve:  curve{{0, 100}, {2, 70}, {3, 60}, {5, 40}, {6, 28}, {10, 0}},
		weight: 8,
		advice: advice{
			bad: "Зробіть паузу в нових кредитах: показник відновиться сам за 6 місяців без нових оформлень.",
		},
		format: credits,
	},
	{
		key:    ParamDebtLoad,
		title:  "Кредитне навантаження",
		hint:   "Скільки з вашого доходу вже забирають щомісячні платежі.",
		bands:  Bands{Bad: "більше 60%", Medium: "30–60%", Good: "менше 30%"},
		level:  lowerIsBetter(30, 60),
		curve:  curve{{0, 100}, {30, 67}, {60, 34}, {100, 0}},
		weight: 12,
		advice: advice{
			bad:    "Платежі забирають забагато від доходу. Допоможе дострокове погашення або відмова від нового кредиту.",
			medium: "Навантаження помітне. Дострокове погашення навіть невеликої суми зменшить щомісячний платіж.",
		},
		format: percentDisplay,
	},
	{
		key:    ParamInquiries,
		title:  "Звернення за кредитами",
		hint:   "Часті заявки на кредит виглядають як пошук грошей будь-де.",
		bands:  Bands{Bad: "більше 15", Medium: "5–15", Good: "менше 5"},
		level:  lowerIsBetter(5, 15),
		curve:  curve{{0, 100}, {5, 67}, {15, 34}, {30, 0}},
		weight: 10,
		advice: advice{
			bad: "Не подавайте заявки «про всяк випадок» — кожна з них видима кредиторам протягом 6 місяців.",
		},
		format: countDisplay("звернення", "звернення", "звернень"),
	},
}

// levelOf uses the indicator's band rule so flags and displayed levels use the same
// thresholds.
func levelOf(k ParamKey, v float64) Level {
	for _, in := range indicators {
		if in.key == k {
			return in.level(v)
		}
	}
	return LevelUnknown
}

// scoreIndicators scores the available measurements. Missing values are marked unknown,
// and the remaining weights are normalized over the measured indicators.
func scoreIndicators(values map[ParamKey]float64) []Parameter {
	measuredWeight := 0
	for _, in := range indicators {
		if _, ok := values[in.key]; ok {
			measuredWeight += in.weight
		}
	}

	params := make([]Parameter, 0, len(indicators))
	for i, in := range indicators {
		p := Parameter{
			Key:    in.key,
			Order:  i + 1,
			Title:  in.title,
			Level:  LevelUnknown,
			Weight: in.weight,
			Bands:  in.bands,
			Hint:   in.hint,
		}
		if v, ok := values[in.key]; ok {
			p.Value = round1(v)
			p.Display = in.format(v)
			p.Level = in.level(v)
			p.Points = round1(in.curve.eval(v))
			p.Advice = in.advice.forLevel(p.Level)
			p.Contribution = round1(p.Points * float64(in.weight) / float64(measuredWeight))
		}
		params = append(params, p)
	}
	return params
}
