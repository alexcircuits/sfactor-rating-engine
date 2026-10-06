# A — Правила рейтинга: порог, терминальные долги, карты, МФО, мониторинг

**Spec:** [2026-08-24-ubki-rating-rules-design.md](../specs/2026-08-24-ubki-rating-rules-design.md)

**Goal:** Сделки, которые УБКІ считает живым долгом, перестают быть невидимыми для девяти показателей; копеечный остаток перестаёт читаться как дефолт; клиент видит расклад по МФО и список наблюдающих за ним компаний.

**Architecture:** Движок остаётся единственным источником арифметики. Все пять правил — правки в `scoring/rating` и `scoring/ubki`; приложение получает три новых поля в JSON и только рисует их. Ни `MFOProfile`, ни `Monitoring`, ни флаг `mfo_pressure` не входят в `score` — инвариант «клиент пересчитывает рейтинг на бумаге» не трогается.

**Tech Stack:** Go 1.26 (`scoring/`), TypeScript, React Native (Expo SDK 57), Jest.

---

## Порядок и зависимости

Задачи 1–3 правят классификацию сделок и обязаны идти **до** задачи 7
(пересчёт фикстур). Задачи 5 и 6 независимы друг от друга и от 1–3. Задачи 8–10
зависят от контракта, зафиксированного в 5, 6 и 7.

```
1 (порог) ─┐
2 (терм.)  ├─→ 4 (report6) ─→ 7 (пересчёт+README) ─→ 8 (TS-контракт) ─→ 9 (экран)
3 (карты) ─┘                                    └─→ 10 (демо-фикстуры)
5 (МФО)   ─────────────────────────────────────────┘
6 (монит.)─────────────────────────────────────────┘
```

---

## File Structure

- Modify: `scoring/rating/config.go` — `OverdueIgnoreAmount`
- Modify: `scoring/rating/deals.go` — терминальные статусы, правило карты, `lastOverdue`, восстановление суммы
- Modify: `scoring/rating/features.go` — фильтр порога, `MFOProfile`, флаг
- Modify: `scoring/rating/result.go` — `MFOProfile`, `MonitoringEntry`, `FlagMFOPressure`
- Modify: `scoring/rating/rate.go` — заполнение новых блоков
- Modify: `scoring/ubki/types.go` — `MonCredRes.MonID`, `.Result`
- Modify: `scoring/ubki/parse.go` — доступ к комп. 6, если его нет
- Create: `scoring/rating/testdata/report6.xml`
- Modify: `scoring/rating/{deals,features,fixtures,rate,format}_test.go`
- Modify: `scoring/README.md` — таблица фикстур, описание правил
- Modify: `src/lib/rating.ts` — типы + селекторы
- Modify: `src/lib/rating.test.ts`
- Modify: `src/lib/ratingDemo.ts` — демо-фикстуры
- Modify: `src/app/score.tsx` — два новых блока

---

## Task 1: Порог просрочки 10 ₴

**Files:** `scoring/rating/config.go`, `scoring/rating/features.go`, `scoring/rating/deals.go`
**Test:** `scoring/rating/features_test.go`

- [x] **Step 1 — падающий тест.** В `features_test.go` покрыть границы: `dlamtexp` = 9.99 (не просрочка), 10.00 (не просрочка, строгое `>`), 10.01 (просрочка). Отдельным подтестом — **согласие показателей 3 и 4**: сумма показателя 4 равна сумме `dlamtexp` ровно тех сделок, что посчитаны в показателе 3, ни больше ни меньше. Отдельным — `lastOverdue` игнорирует снимок с остатком 4 ₴ и не сбрасывает серию показателя 2.
- [x] **Step 2 — реализация.** `Config.OverdueIgnoreAmount = 10` в `DefaultConfig()` с комментарием в стиле файла. В `extractPortfolio` завести один предикат `isOverdue(snapshot, cfg)` и использовать **его же** в счётчике и в сумме — не два независимых условия. Тот же предикат прокинуть в `lastOverdue` (сигнатура получает порог).
- [x] **Step 3 — проверка.** `go test ./...` зелёный. Ни одна из пяти боевых фикстур не должна поменять счёт: суммы там 1400.00 / 1500.00 / 1034.62. Если счёт поехал — разобраться, а не править ожидания.

---

## Task 2: Проданный и списанный долг становится живым

**Files:** `scoring/rating/deals.go`, `scoring/rating/features.go`
**Test:** `scoring/rating/deals_test.go` (создать, если нет), `scoring/rating/features_test.go`

- [x] **Step 1 — падающий тест.** `newDealView` на статусах 1/2/3/4/13: 3 и 13 дают `open = true`, `repaid = false`, `terminal = true`. Цепочка восстановления суммы — три подтеста: есть `dlamtexp` до терминального снимка → берётся он; `dlamtexp` везде 0, но есть `dlamtcur` → берётся `dlamtcur`; оба нулевые → 0, но сделка **всё равно** в счётчике показателя 3.
- [x] **Step 2 — реализация.** В `dealView` добавить поле `terminal bool` и `terminalOverdue float64`. В `newDealView` расширить `switch`: `statusSold, statusWriteOff` → `open = true`, `terminal = true`, сумма из новой функции `recoverTerminalAmount(d)`. В `extractPortfolio` терминальная сделка всегда инкрементит `overdueDeals` и добавляет `terminalOverdue` в сумму **в обход** порога из задачи 1 (0 ₴ там значит «неизвестно», а не «мало»).
- [x] **Step 3 — проверка.** `go test ./rating/...`. Фикстурные тесты сейчас упадут — это ожидаемо, они чинятся в задаче 7. Убедиться, что упали именно `report1` и `report2` и именно по счётчикам сделок.

---

## Task 3: Кредитная карта с живым лимитом

**Files:** `scoring/rating/deals.go`
**Test:** `scoring/rating/deals_test.go`

- [x] **Step 1 — падающий тест.** Карта `dlcelcred="31"`, статус 2, последний снимок `dlamtlim=15000` → `open = true`, `repaid = false`. Карта `dlcelcred="31"`, статус 2, последний снимок `dlamtlim=0`, но ранний снимок `dlamtlim=300` → остаётся погашенной (правило смотрит только на последний снимок — случай `report5`). Не-карта (`dlcelcred="7"`) со статусом 2 и лимитом → остаётся погашенной. **Приоритет:** карта со статусом 3 и живым лимитом → правило A2 сильнее, сделка терминальная и просроченная.
- [x] **Step 2 — реализация.** Константа `celCredCreditCard = "31"` рядом с существующими кодами статусов. Правило применяется в `newDealView` **после** ветки терминальных статусов и только если она не сработала.
- [x] **Step 3 — проверка.** `go test ./rating/...`; `report5` не должен измениться.

---

## Task 4: Синтетическая фикстура `report6.xml`

**Files:** `scoring/rating/testdata/report6.xml`
**Test:** `scoring/rating/fixtures_test.go`

- [x] **Step 1.** Собрать XML по образцу существующих фикстур (шаблон 10, `<tech><trace><step name="build report" stm="2026-07-08 …">`). Содержимое — из раздела 8 спеки: две мелкие просрочки (7.40 ₴ и ровно 10.00 ₴), карта `dlcelcred=31` статус 2 с последним `dlamtlim=15000`, две действующие сделки `dldonor="MFO"`, блок `moncredres` с одной записью `enddate` **позже** 2026-07-08.
- [x] **Step 2.** Добавить в `fixtures_test.go` по образцу существующих записей. Ожидания вписываются **после** прогона, из фактического вывода, с проверкой глазами, что каждое число объяснимо.
- [x] **Step 3.** `go run ./cmd/score -table rating/testdata/report6.xml` — таблица читается и не противоречит сама себе.

---

## Task 5: Анализ МФО и флаг `mfo_pressure`

**Files:** `scoring/rating/result.go`, `scoring/rating/features.go`, `scoring/rating/rate.go`
**Test:** `scoring/rating/features_test.go`, `scoring/rating/rate_test.go`

- [x] **Step 1 — падающий тест.** `MFOProfile` считает сделки и обращения по `BNK`/`MFO`/`FIN`. `Inquiries6m` **равен** значению показателя 9 в том же ответе — тест сравнивает два поля и падает при расхождении. Флаг: 16 обращений → зажигается; ровно 15 → нет; 2 действующих займа МФО → зажигается; 1 → нет; ключа `OWN` в `InquiriesByOrg` нет никогда.
- [x] **Step 2 — реализация.** `FlagMFOPressure = "mfo_pressure"` в блоке констант флагов `result.go`. Сбор профиля — новый метод в `features.go`, переиспользующий `isCreditApplication` и окно `cfg.RecentWindowDays`; **не** заводить второй фильтр обращений. Флаг дописывается в `reportFlags`.
- [x] **Step 3 — проверка.** `report2` (17 обращений) получает флаг; `report1` (15) — нет.

---

## Task 6: Мониторинг банков

**Files:** `scoring/ubki/types.go`, `scoring/rating/result.go`, `scoring/rating/rate.go`
**Test:** `scoring/ubki/parse_test.go`, `scoring/rating/rate_test.go`

- [x] **Step 1 — падающий тест.** Парсер достаёт `monid` и `result` из `<moncredres>`. `MonitoringEntry` сортируется по `StartDate` убыванием; `Active` истинен при `end_date >= as_of` и ложен при `end_date < as_of`. `org="BCH"` получает метку «Бюро», неизвестный код — сам код, а не пустую строку.
- [x] **Step 2 — реализация.** Поля в `ubki.MonCredRes`. `MonitoringEntry` и карта меток в `result.go`. Заполнение в `rate.go` из `r.Comp(6)`. Отдаются **все** записи — фильтра по `Active` нет (на боевых фикстурах активных ноль, фильтр дал бы пустой блок везде).
- [x] **Step 3 — проверка.** На `report1` — 11 записей, активных 0. На `report6` — активная есть.

---

## Task 7: Пересчёт фикстур и README движка

**Files:** `scoring/rating/fixtures_test.go`, `scoring/README.md`

- [x] **Step 1.** `go test ./... 2>&1` — собрать фактические числа по всем шести отчётам.
- [x] **Step 2.** Для **каждого** изменившегося числа записать в описание фикстуры, какое правило его сдвинуло. Число, которое не удаётся объяснить правилом, — сигнал бага в задачах 1–3, а не повод переписать ожидание.
- [x] **Step 3.** Обновить `scoring/README.md`: таблицу «Fixture outcomes» фактическими числами, раздел про потолки (у `report2` теперь `current_overdue`), новый раздел про пять правил, флаг `mfo_pressure` в списке флагов.
- [x] **Step 4.** Сверить с разделом 12 спеки. Каждое расхождение с предсказанием — либо объяснить в README, либо починить код.

---

## Task 8: TypeScript-контракт и селекторы

**Files:** `src/lib/rating.ts`
**Test:** `src/lib/rating.test.ts`

- [x] **Step 1 — падающий тест.** `monitoringSummary(rating)` на пустом мониторинге, на списке без активных, на списке с активными. `donorBreakdown(rating)` при **отсутствующем** блоке `mfo` (старый ответ движка) возвращает `null` и не бросает. Сообщение флага `mfo_pressure` присутствует в `FLAG_MESSAGE`.
- [x] **Step 2 — реализация.** `RatingFlag` получает `'mfo_pressure'`; интерфейсы `MFOProfile`, `MonitoringEntry`; поля `mfo?`, `monitoring?` на `Rating`. Селекторы — чистые функции, экран не считает ничего. Украинский текст флага — из раздела 6 спеки.
- [x] **Step 3.** `npm test` и `npm run typecheck` зелёные.

---

## Task 9: Экран рейтинга

**Files:** `src/app/score.tsx`
**Test:** визуальная проверка + `npm run typecheck`

- [x] **Step 1.** После таблицы показателей — блок «Хто стежить за твоєю кредитною історією»: заголовок с числом компаний, до 5 строк `Row` (метка организации, период, `Pill` «Активний» / «Завершений»), подпись, что на рейтинг не влияет.
- [x] **Step 2.** Блок расклада МФО: сделки и обращения по типам кредиторов. При зажжённом `mfo_pressure` — предупреждение тем же способом, что и существующие `flagMessages`.
- [x] **Step 3.** Оба блока не рендерятся, когда данных нет. Использовать только существующие `Card`/`Row`/`Pill`/`SectionHeader`/`Text` — новой визуальной лексики не вводить.
- [x] **Step 4.** Списанный долг с нулевой суммой не должен выглядеть на экране ошибкой — проверить формулировку на `report1`.

---

## Task 10: Демо-фикстуры приложения

**Files:** `src/lib/ratingDemo.ts`
**Test:** `src/lib/rating.test.ts`, `src/state/reducer.test.ts`

- [x] **Step 1.** Перегенерировать `DEMO_RATING_CLEAN` и `DEMO_RATING_OVERDUE` из фактического вывода движка (`go run ./cmd/score -income 25000 …`), а не править руками.
- [x] **Step 2.** Добавить в них блоки `monitoring` и `mfo`, чтобы демо показывало новые секции.
- [x] **Step 3.** `npm test` зелёный.

---

## Критерии приёмки

- [x] `cd scoring && go test ./...` — зелёный, вывод приложен к отчёту
- [x] `go vet ./...` — чисто
- [x] `npm test` и `npm run typecheck` — зелёные
- [x] `npm run lint` — чисто
- [x] Числа в `scoring/README.md` совпадают с фактическим прогоном
- [x] `score` не изменился ни от `mfo`, ни от `monitoring`, ни от флага — доказано тестом, который считает сумму `contribution` и сверяет с `raw`
- [x] Каждое из пяти правил включается/выключается через `Config` или проверяемо одним тестом
