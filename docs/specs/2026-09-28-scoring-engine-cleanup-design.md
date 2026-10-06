# Scoring engine: a readable pipeline, and one XML-plus-data call for the app's rating

_2026-09-28. Flokzy ENG-687. Design approved in conversation. Replaces the layout section (§8) of
[2026-08-07-fhp-9-parameter-rating-design.md](./2026-08-07-fhp-9-parameter-rating-design.md); the
rating model itself (the nine indicators, curves, bands, weights, ceilings and the deal rules of
[2026-08-24-ubki-rating-rules-design.md](./2026-08-24-ubki-rating-rules-design.md)) does not change._

## 1. What this changes

The Go engine in `scoring/` works and is well tested (95% statement coverage in `rating`), but it
reads as accumulated rather than designed:

- Go fields repeat the XML attribute names (`DLAmtPaym`, `DLFlStat`, `DLCelCred`), so every rule has
  to be decoded before it can be read.
- `features` is one struct filled by six methods that must run in a particular order ("the flags read
  the breakdown, so they come last").
- An indicator is defined in three places: its table entry, its weight in `Config.Weights`, its
  advice in `advice.go`.
- The terminal-debt rationale is written out five times; about 60% of the УБКІ model (names, INN,
  addresses, documents, wanted/sanctions/bankruptcy/gambling registries) is parsed and never read.
- `POST /rating` takes the XML as a raw body and the borrower's income in the query string, where
  proxies and gateways log it.

Review also confirmed defects, each reproduced by running the code (§7.2).

This work restructures the engine so a reader can follow a rating from XML to JSON top to bottom,
and turns `POST /rating` into the one call a backend makes: the УБКІ XML file plus the borrower's
data in, the exact `Rating` object the app renders (`src/lib/rating.ts`) out.

## 2. Decisions

| # | Decision | Why |
|---|----------|-----|
| 1 | Restructure in place: keep the three layers (УБКІ parser → rating → HTTP) | The layering is right; the problem is inside each layer. A declarative rules engine would add indirection for 9 indicators and 3 ceilings |
| 2 | Behaviour is pinned by golden JSON captured from today's code before the first edit | A refactor of this size is only safe if every output byte is compared, not just the fields a test happens to check |
| 3 | The app's JSON contract does not change: same keys, types and numbers | The app mirrors it field for field in `src/lib/rating.ts` and must not need an update |
| 4 | Money is integer kopiykas from the parse boundary on | Float sums miss exact thresholds (§7.2); the app already treats money this way |
| 5 | `POST /rating` keeps its path and takes `multipart/form-data` | Six documents and app comments already name the path. Multipart is literally "an XML file plus fields", and it takes income out of the URL |
| 6 | Unknown form fields are rejected | A caller still sending `income` would otherwise silently lose the debt-load indicator |
| 7 | App-side client code is out of scope | Owner's choice: nothing in the app can supply the XML yet (Mobile API v0.1.0 has no credit-history route) |

## 3. Layout

```
scoring/
  ubki/               the part of УБКІ template 10 the rating reads, parsed leniently
    doc.go            what the report is; the leniency policy
    report.go         Report, its sections, Parse, one accessor per section read
    codes.go          УБКІ code vocabulary: deal status, role, purpose, creditor type, …
    values.go         lenient attribute types: Int, Money (kopiykas), Date
  rating/             the nine-indicator rating
    doc.go            what the rating is and is not; the pipeline in one paragraph
    rate.go           Input, Rate: the pipeline, top to bottom
    indicators.go     the nine indicators, one table entry each
    deals.go          which deals count, and as what
    measure.go        the nine values
    ceilings.go       the three ceilings
    reference.go      flags, lender breakdown, monitoring: shown, never scored
    result.go         the JSON contract
    config.go         thresholds and windows
    curve.go, bands.go, format.go
    testdata/         six УБКІ reports + golden/ payloads
  internal/httpapi/   POST /rating, GET /health
  cmd/server/         flags, logging, graceful shutdown; wires httpapi
  cmd/score/          CLI: JSON or a table
  api/openapi.yaml    the HTTP contract
```

The fixtures stay in `rating/testdata/`: app comments and docs quote that path.

## 4. `ubki`

**Only what is read.** `Report` models the build trace, deals (comp 2), declared employment
(comp 1, the income fallback), enforcement proceedings (comp 3), the inquiry registry (comp 4),
monitoring (comp 6) and the bureau's obligation total (comp 78). Everything else is dropped and
recoverable from git history. `encoding/xml` skips what is not modelled, so personal data the rating
never uses is no longer decoded into structs.

**Names say what things are; tags say where they come from.**

```go
type Snapshot struct {
    ReportedOn  Date       `xml:"dldateclc,attr"`
    Status      DealStatus `xml:"dlflstat,attr"`
    StartedOn   Date       `xml:"dlds,attr"`
    DaysOverdue Int        `xml:"dldayexp,attr"`
    Overdue     Money      `xml:"dlamtexp,attr"`
    Balance     Money      `xml:"dlamtcur,attr"`
    Payment     Money      `xml:"dlamtpaym,attr"`
    Limit       Money      `xml:"dlamtlim,attr"`
}
```

**Codes are named** in `codes.go`: `StatusOpen/Closed/Sold/Restructured/WrittenOff`,
`RoleBorrower/Guarantor`, `PurposeCreditCard`, `CreditorBank/MFO/Finance/Bureau/Own`,
`ReasonCredit/OnlineCredit`, `ReportTypeIdentity`. `ubki` owns the vocabulary of the format;
`rating` owns the policy.

**Lenient values.** Unreadable attributes degrade to zero instead of failing the document:

- `Int`: a named int; no accessor methods.
- `Money`: `int64` kopiykas. `ParseMoney(string) (Money, error)` is the strict parser (also used for
  the API's income field); rounds to the nearest kopiyka; rejects NaN, ±Inf and magnitudes above
  10¹² ₴. `const Hryvnia Money = 100` lets thresholds read `10 * ubki.Hryvnia`. `Hryvnias()` converts
  back for display.
- `Date`: a calendar date with an unexported field. `Time() (time.Time, bool)` forces the caller to
  handle "unknown" (empty, unreadable, or a placeholder such as `1900-01-01`); `String()` is
  `YYYY-MM-DD` or empty. It no longer embeds `time.Time`, whose promoted methods compared unknown
  dates against year 1 without complaint.

**Accessors**, one per section read, so `rating` never mentions a comp id: `Deals()`,
`Inquiries() ([]Inquiry, bool)` (false: no registry at all, which is "unknown", not "none"),
`Monitoring()`, `ActiveEnforcements()`, `DeclaredIncome()`, `MonthlyObligations()`,
`BuiltAt() (time.Time, bool)`.

**`BuiltAt`** prefers the finish time of the `build report` trace step, then any readable step, then
the date of the report's own `OWN` inquiry. It no longer falls back to `time.Now` itself: the caller
decides. `trimFrac` goes; `time.Parse` already accepts fractional seconds.

**`Deal.Latest`**: the bureau writes snapshots chronologically, so file order decides, except that a
snapshot dated before one already seen never becomes the latest. Ties go to the later entry. (Today's
rule is not an order: `[May, undated, March]` returns March.)

## 5. `rating`

### 5.1 The pipeline

```go
type Input struct {
    Report        *ubki.Report
    MonthlyIncome ubki.Money // verified, from the accounting system; zero when unknown
    AsOf          time.Time  // reference date; zero means the report's build date
}

func Rate(in Input, cfg Config) Result
```

`Rate` reads top to bottom, each step a function returning a value:

1. **Reference date**: `in.AsOf`, else `Report.BuiltAt()`, else today; always reduced to its
   calendar date (the date in the time's own location, as UTC midnight).
2. **Classify deals** (`deals.go`): the borrower's own deals, each with one `standing`.
3. **Measure** (`measure.go`): the nine values; an indicator that cannot be measured is absent.
4. **Score** (`indicators.go`): points, colour, contribution; weights renormalized over what was
   measured.
5. **Ceiling** (`ceilings.go`): at most one, strictest first; it only ever lowers the score.
6. **Reference blocks** (`reference.go`): flags, lender breakdown, monitoring. Never scored.

`AsOf` moves from `Config` to `Input`: it describes the request, not a tunable. `Config` keeps only
thresholds and windows, with months as `int` calendar months and amounts as `ubki.Money`. Field
names stay as they are, since the specs and README cite them.

### 5.2 One table entry per indicator

```go
type indicator struct {
    key    ParamKey
    title  string
    hint   string
    bands  Bands                // the printed ranges behind each colour
    level  bandRule             // the same ranges, as numbers
    curve  curve                // value → 0–100 points
    weight int                  // percent; the nine sum to 100
    advice advice               // what to do while the value is not green
    format func(float64) string
}
```

`Config.Weights` and `advice.go` fold into the table. The band copy stays hand-written (it is agreed
wording with irregular forms such as «2 і більше»); `TestLevelBoundariesMatchTheAgreedTable` keeps
pinning it to the numeric rule.

### 5.3 Deals

A deal's state is one enum instead of three booleans with implied rules:

| `standing` | From the latest snapshot | Open (5) | Repaid (6) |
|---|---|---|---|
| `active` | status 1 or 4; or a credit card (purpose 31) still carrying a limit | yes | no |
| `terminal` | status 3 (sold) or 13 (written off) | yes | no |
| `repaid` | status 2, never sold or written off | no | yes |
| `other` | status 2 after a sale or write-off; unknown codes | no | no |

Terminal beats the card rule, as today. Guarantor deals never reach the list.

**One terminal date per deal.** `terminalEvent` is the most recent sold/written-off snapshot, and an
undated one anywhere makes the event count as recent. Both the "terminal debt is current arrears"
rule and the two terminal ceilings ask it, through one method:
`within(asOf, months) = happened && (undated || at ≥ asOf − months)`, calendar months.

**One arrears predicate.** `snapshotOverdue(s) = DaysOverdue > 0 && Overdue > OverdueIgnoreAmount`
decides indicators 3 and 4 for active deals and every episode the clean streak looks back to. A
terminal deal is in arrears while its event is within `TerminalLookbackMonths`, at the amount
recovered from the last snapshot that reported one, exactly as today.

### 5.4 Result types

The JSON shape is untouched. Go-side typing improves: `Flag`, `CapReason`, `IncomeSource` and
`Reason` become typed strings. `ParamOrder()` (unused) and the unreachable branch in
`overdueCapApplies` go.

### 5.5 Comments

Each rule's reason is stated once, in the doc comment of the function that implements it. History
("was 37 before the terminal-debt rule") lives in git and in the specs, not in code or the README.

## 6. HTTP API

### 6.1 `POST /rating`

`Content-Type: multipart/form-data`. Request body at most 4 MiB.

| Part | Required | Content |
|---|---|---|
| `report` | yes | The УБКІ XML report, template 10, UTF-8 |
| `monthly_income` | no | Verified monthly income in hryvnias as a decimal (`25000`, `25000.50`), rounded to the kopiyka; negative or unreadable is a 400. Absent or `0`: the engine falls back to a self-declared УБКІ income no older than 12 months (`income_source: "ubki"`), and failing that debt load is unmeasured and its weight moves to the other indicators |
| `as_of` | no | Reference date `YYYY-MM-DD`. Default: the report's build date. A later date ages the report; an earlier one is a 400 (`rating.Input.Validate`), because the engine reads the report as it stands and cannot rewind it |

Any other part is rejected, and so is a repeated one. An empty optional part reads as an absent
one, since HTML forms send blank fields. `OPTIONS /rating` answers `204` (CORS preflight).

`200 OK`: the `Rating` object of `src/lib/rating.ts`, byte for byte what `rating.Rate` marshals.
Headers: `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store` (credit data),
`X-Content-Type-Options: nosniff`.

Errors: `{"error": "<code>", "message": "<English sentence>"}`. Report content is never echoed; XML
parse errors are logged server-side only.

| Status | `error` | When |
|---|---|---|
| 400 | `invalid_request` | Malformed multipart; unknown or repeated part; bad `monthly_income` or `as_of` (the message names the field); an `as_of` before the report was built |
| 400 | `missing_report` | No `report` part, or an empty one |
| 400 | `invalid_report` | The report is not УБКІ XML |
| 405 | `method_not_allowed` | Any method but POST (and OPTIONS); `Allow: POST, OPTIONS` |
| 413 | `report_too_large` | The body exceeds 4 MiB. Only then: other read errors are 400 |
| 415 | `unsupported_media_type` | Not `multipart/form-data` |

### 6.2 `GET /health`

`200 {"status":"ok"}`, unchanged.

### 6.3 Unchanged operational behaviour

Unauthenticated by design (it sits behind the gateway). CORS headers only when `-cors-origin` names
the one allowed browser origin. Read/write timeouts and graceful shutdown on SIGTERM as today.
Logging moves to `log/slog` (JSON), never carrying report content or income.

### 6.4 Ready to use

`api/openapi.yaml` describes both endpoints and the full `Rating` schema (it loads into Swagger UI or
Postman with a file picker). The README carries the two calls a backend needs:

```bash
curl -F report=@report.xml -F monthly_income=25000 http://localhost:8080/rating
```

```js
const form = new FormData();
form.set('report', new Blob([reportXml], { type: 'application/xml' }), 'report.xml');
form.set('monthly_income', '25000');
const res = await fetch(`${SCORING_URL}/rating`, { method: 'POST', body: form });
const rating = await res.json(); // src/lib/rating.ts `Rating`, or { error, message }
```

A test checks that every key in the golden payloads is documented in `openapi.yaml`, so the spec
cannot fall behind the code.

## 7. Behaviour

### 7.1 Pinned

Golden payloads, captured from today's code in the first commit and required byte-identical after
every later one:

- all six fixtures at `as_of = 2026-07-08`, no income;
- `report3` with income 24 000 ₴ (the source of the app's `DEMO_RATING_CLEAN`);
- `report2` at `2027-07-08`, `2028-07-08` and `2031-07-08` (the terminal-debt ageing walk).

A second test asserts that each fixture rated with its own build date equals the same fixture rated
with `as_of = 2026-07-08`.

### 7.2 Fixed on purpose

Each fix gets a test that fails before it. None of them moves a golden payload: no fixture sits on
any of these edges.

| # | Defect | Reproduction | Fix |
|---|--------|--------------|-----|
| 1 | T0 keeps the report's time of day | `report6` with a monitoring subscription ending on the build date: `active: false` by default, `true` with `-as-of` of the same date. The 183-day windows shift the same way | T0 is a calendar date |
| 2 | Float sums miss exact thresholds | Arrears of 10.14 + 58.12 + 31.74 ₴ sum to 99.999999999999986: no 100 ₴ ceiling, and a green indicator 4 displayed as "100 ₴". Arrears of 11.44 + 512.19 + 476.37 ₴ land above 1 000 and paint it red | Integer kopiykas |
| 3 | Months are 30.44 days | A debt sold on 2025-04-03 is outside the 36-month window on 2028-04-03 (1 096 days ÷ 30.44 = 36.005) | Calendar months (`AddDate`) |
| 4 | Two notions of "when the debt went terminal" | The ceilings read any terminal snapshot; the arrears rule reads the latest snapshot's date | One `terminalEvent` per deal |
| 5 | `Latest` is not an order | `[May, undated, March]` returns March | §4 rule |
| 6 | Two "unavailable" shapes | A nil report returns an empty `band` and `as_of`, which the app's type does not allow; a report without deals returns `band: "low"` | One shape |
| 7 | 413 for any body read error | A client disconnect is reported as "too large" | 413 only for `http.MaxBytesError` |
| 8 | An `as_of` before the report silently counts later snapshots (found in code review) | Snapshots Jan clean, Mar 90 days overdue: rated as of 1 Feb, the file shows one overdue loan at 5 000 ₴ | Refused: `rating.Input.Validate`, a 400 from the API, an error from the CLI |

### 7.3 Deliberately unchanged

Weights, curves, bands, ceilings, flags and every deal rule. Also unchanged, and left to a product
decision: the display rounds where the colour does not, so 99.60 ₴ shows "100 ₴" in green beside
«менше 100 ₴», and 29.96% shows "30%" in green beside «менше 30%».

## 8. Testing

- **Golden payloads** (§7.1), regenerated only with `go test ./rating -update` and reviewed as a diff.
- **Every existing scenario survives**, ported to the new internals: the rule tests document the
  model and are kept, their narrative trimmed.
- **New tests** for each fix in §7.2, for `Money` parsing (exact kopiykas; NaN, Inf and huge values
  degrade to zero; the strict parser errors), for `Date` and for `Latest`.
- **HTTP**: a multipart request equals the golden payload; one test per error row in §6.1; CORS;
  `Cache-Control`; no report content in any error body.
- **CLI**: `run(args, stdout, stderr)` replaces the body of `main`, so JSON and table output are
  tested.
- `go vet`, `go test -race ./...`, golangci-lint clean.

## 9. Out of scope

- App code, including a TS client (owner's choice). App comments name `POST /rating` by path only,
  and the path does not change.
- Where the XML comes from: Mobile API v0.1.0 has no credit-history route.
- Recalibration of any weight, curve or threshold.
- The knockout layer the nine-indicator model dropped (wanted lists, sanctions, bankruptcy).
