package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	KnownCurrencies = map[string]bool{
		"BTC":   true,
		"ETH":   true,
		"STORJ": true,
		"XLM":   true,
		"XMR":   true,
		"ZEC":   true,
		"ADA":   true,
		"ETC":   true,
	}
)

type TaxEvent struct {
	Date                         string
	Amount                       *big.Rat
	Currency                     string
	SalePricePerUnitInUSD        *big.Rat
	AverageCostBasisPerUnitInUSD *big.Rat
	LongTerm                     bool
	AcquisitionDates             map[string]bool
}

type Holding struct {
	Currency              string
	Amount                *big.Rat
	CostBasisPerUnitInUSD *big.Rat
	AcquisitionDate       time.Time
	Tags                  string
}

type HoldingDB struct {
	Holdings  []*Holding
	Balances  map[string]*big.Rat
	TaxEvents map[string][]*TaxEvent
}

func NewHoldingDB() *HoldingDB {
	db := &HoldingDB{
		Balances:  map[string]*big.Rat{},
		TaxEvents: map[string][]*TaxEvent{},
	}
	for currency := range KnownCurrencies {
		db.Balances[currency] = big.NewRat(0, 1)
	}
	return db
}

func parseTime(date string) (time.Time, error) {
	if strings.Contains(date, "/") {
		return time.Parse("2006/01/02", date)
	}
	return time.Parse("2006-01-02", date)
}

func (h *HoldingDB) Buy(currency, amount, costBasisPerUnitInUSD, acquisitionDate, tags string) {
	if !KnownCurrencies[currency] {
		panic("unknown currency")
	}
	date, err := parseTime(acquisitionDate)
	if err != nil {
		panic(err)
	}
	holding := &Holding{
		Currency:              currency,
		Amount:                must(new(big.Rat).SetString(amount)),
		CostBasisPerUnitInUSD: must(new(big.Rat).SetString(costBasisPerUnitInUSD)),
		AcquisitionDate:       date,
		Tags:                  tags,
	}

	h.Holdings = append(h.Holdings, holding)
	h.Balances[currency].Add(h.Balances[currency], holding.Amount)
}

func (h *HoldingDB) Sell(currency, amount, salePricePerUnitInUSD, saleDate, tags string) {
	if !KnownCurrencies[currency] {
		panic("unknown currency")
	}
	date, err := parseTime(saleDate)
	if err != nil {
		panic(err)
	}

	var amountVal, salePrice big.Rat
	must(amountVal.SetString(amount))
	must(salePrice.SetString(salePricePerUnitInUSD))

	h.Balances[currency].Sub(h.Balances[currency], &amountVal)
	if h.Balances[currency].Sign() < 0 {
		panic("negative balance")
	}

	sort.Sort(HoldingsByCurrencyAndDate{currency, h.Holdings})

	remainingAmountVal := new(big.Rat).Set(&amountVal)
	var longTermCostBasisSum, shortTermCostBasisSum big.Rat
	var longTermAmount, shortTermAmount big.Rat
	longTermAcquisitionDates := map[string]bool{}
	shortTermAcquisitionDates := map[string]bool{}

	for remainingAmountVal.Sign() > 0 {
		if len(h.Holdings) <= 0 {
			panic("no more holdings")
		}
		next := h.Holdings[len(h.Holdings)-1]
		if next.Currency != currency {
			panic("no more holdings of currency")
		}

		if less(remainingAmountVal, next.Amount) {
			// this one has more than enough to cover it
			next.Amount.Sub(next.Amount, remainingAmountVal)
			if next.Amount.Sign() < 0 {
				panic("error!")
			}

			costBasisSum := new(big.Rat).Mul(next.CostBasisPerUnitInUSD, remainingAmountVal)
			if isLongTerm(next.AcquisitionDate, date) {
				longTermCostBasisSum.Add(&longTermCostBasisSum, costBasisSum)
				longTermAmount.Add(&longTermAmount, remainingAmountVal)
				longTermAcquisitionDates[next.AcquisitionDate.Format("2006-01-02")] = true
			} else {
				shortTermCostBasisSum.Add(&shortTermCostBasisSum, costBasisSum)
				shortTermAmount.Add(&shortTermAmount, remainingAmountVal)
				shortTermAcquisitionDates[next.AcquisitionDate.Format("2006-01-02")] = true
			}

			break
		}

		remainingAmountVal.Sub(remainingAmountVal, next.Amount)
		costBasisSum := new(big.Rat).Mul(next.CostBasisPerUnitInUSD, next.Amount)
		if isLongTerm(next.AcquisitionDate, date) {
			longTermCostBasisSum.Add(&longTermCostBasisSum, costBasisSum)
			longTermAmount.Add(&longTermAmount, next.Amount)
			longTermAcquisitionDates[next.AcquisitionDate.Format("2006-01-02")] = true
		} else {
			shortTermCostBasisSum.Add(&shortTermCostBasisSum, costBasisSum)
			shortTermAmount.Add(&shortTermAmount, next.Amount)
			shortTermAcquisitionDates[next.AcquisitionDate.Format("2006-01-02")] = true
		}
		h.Holdings = h.Holdings[:len(h.Holdings)-1]
	}
	if remainingAmountVal.Sign() < 0 {
		panic("error!")
	}

	if longTermAmount.Sign() > 0 {
		h.TaxEvents[saleDate] = append(h.TaxEvents[saleDate], &TaxEvent{
			Date:                         saleDate,
			Amount:                       &longTermAmount,
			Currency:                     currency,
			SalePricePerUnitInUSD:        &salePrice,
			AverageCostBasisPerUnitInUSD: new(big.Rat).Quo(&longTermCostBasisSum, &longTermAmount),
			LongTerm:                     true,
			AcquisitionDates:             longTermAcquisitionDates,
		})
	}
	if shortTermAmount.Sign() > 0 {
		h.TaxEvents[saleDate] = append(h.TaxEvents[saleDate], &TaxEvent{
			Date:                         saleDate,
			Amount:                       &shortTermAmount,
			Currency:                     currency,
			SalePricePerUnitInUSD:        &salePrice,
			AverageCostBasisPerUnitInUSD: new(big.Rat).Quo(&shortTermCostBasisSum, &shortTermAmount),
			LongTerm:                     false,
			AcquisitionDates:             shortTermAcquisitionDates,
		})
	}

}

func (h *HoldingDB) Trade(currency1, currency2, amount1, amount2,
	sourceCurrencyPricePerUnitInUSD, targetCurrencyPricePerUnitInUSD,
	tradeDate string) {
	if sourceCurrencyPricePerUnitInUSD != "" {
		if targetCurrencyPricePerUnitInUSD != "" {
			panic("needs only one currency price")
		}

		var a1, p, a2, d big.Rat
		must(a1.SetString(amount1))
		must(p.SetString(sourceCurrencyPricePerUnitInUSD))
		must(a2.SetString(amount2))
		d.Quo(new(big.Rat).Mul(&a1, &p), &a2)

		h.Sell(currency1, amount1, sourceCurrencyPricePerUnitInUSD, tradeDate, "trade-to-"+currency2)
		h.Buy(currency2, amount2, d.String(), tradeDate, "trade-from-"+currency1)
	} else {
		if targetCurrencyPricePerUnitInUSD == "" {
			panic("needs currency price")
		}

		var a1, p, a2, d big.Rat
		must(a2.SetString(amount2))
		must(p.SetString(targetCurrencyPricePerUnitInUSD))
		must(a1.SetString(amount1))
		d.Quo(new(big.Rat).Mul(&a2, &p), &a1)

		h.Sell(currency1, amount1, d.String(), tradeDate, "trade-to-"+currency2)
		h.Buy(currency2, amount2, targetCurrencyPricePerUnitInUSD, tradeDate, "trade-from-"+currency1)
	}
}

func rowEqual(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (h *HoldingDB) LoadCSV(r io.Reader) error {
	source := csv.NewReader(r)

	header1, err := source.Read()
	if err != nil {
		return err
	}

	if !rowEqual(header1, strings.Split(",Buy,,,,,Trades,,,,,,,Transfers,,,,,,,Sales,,,,,,", ",")) {
		return fmt.Errorf("malformed csv")
	}

	header2, err := source.Read()
	if err != nil {
		return err
	}

	if !rowEqual(header2, strings.Split(",Amount,Currency,Unit basis,USD Value,,Amount,Source currency,Amount,Target currency,Unit price,Target amount after fees,,Amount,Currency,Source,Target,Fees (in addition to Amount),,,Amount,Currency,Unit price,Fees (in addition to Amount),USD Net,,URL", ",")) {
		return fmt.Errorf("malformed csv")
	}

	for {
		row, err := source.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		date := row[0]
		if date == "" {
			return fmt.Errorf("invalid date")
		}

		rowType := ""

		// buy?
		{
			amount := cleanAmount(row[1])
			currency := row[2]
			unitbasis := cleanAmount(row[3])

			if amount != "" || currency != "" || unitbasis != "" {
				h.Buy(currency, amount, unitbasis, date, "")
				if rowType != "" {
					panic("row double duty")
				}
				rowType = "buy"
			}
		}

		// trade?
		{
			sourceAmount := cleanAmount(row[6])
			sourceCurrency := row[7]
			targetAmount := cleanAmount(row[8])
			targetCurrency := row[9]
			sourceUnitPrice := cleanAmount(row[10])
			targetAmountAfterFees := cleanAmount(row[11])

			if sourceAmount != "" || sourceCurrency != "" || sourceUnitPrice != "" ||
				targetAmount != "" || targetCurrency != "" || targetAmountAfterFees != "" {
				h.Trade(sourceCurrency, targetCurrency, sourceAmount, targetAmount, sourceUnitPrice, "", date)
				if rowType != "" {
					panic("row double duty")
				}
				rowType = "trade"
			}
		}

		// sell?
		{
			amount := cleanAmount(row[20])
			currency := row[21]
			unitPrice := cleanAmount(row[22])
			fees := cleanAmount(row[23])

			if amount != "" || currency != "" || unitPrice != "" || fees != "" {
				h.Sell(currency, amount, unitPrice, date, "")
				if rowType != "" {
					panic("row double duty")
				}
				rowType = "sell"
			}
		}
	}
}

func main() {
	db := NewHoldingDB()

	if len(os.Args) <= 1 {
		fmt.Printf("usage: %s <trades.csv>\n", os.Args[0])
		os.Exit(1)
	}

	fh, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	err = db.LoadCSV(fh)
	if err != nil {
		panic(err)
	}
	fh.Close()

	fmt.Println("Description\tDate acquired\tDate sold\tProceeds\tCost Basis\tUnit price\tUnit basis\tGain (or loss)\tTerm\n")
	for _, date := range sortedEvents(db.TaxEvents, false) {
		byCurrency := map[string][]*TaxEvent{}
		for _, event := range db.TaxEvents[date] {
			byCurrency[event.Currency] = append(byCurrency[event.Currency], event)
		}
		for _, currency := range sortedEvents(byCurrency, false) {
			byLongTerm := map[string][]*TaxEvent{}
			for _, event := range byCurrency[currency] {
				msg := "short"
				if event.LongTerm {
					msg = "long"
				}
				byLongTerm[msg] = append(byLongTerm[msg], event)
			}

			for _, msg := range sortedEvents(byLongTerm, false) {
				var salesPriceSum, costBasisSum, amount big.Rat

				acquisitionDates := map[string]bool{}
				for _, event := range byLongTerm[msg] {
					salesPriceSum.Add(&salesPriceSum,
						new(big.Rat).Mul(event.Amount, event.SalePricePerUnitInUSD))
					costBasisSum.Add(&costBasisSum,
						new(big.Rat).Mul(event.Amount, event.AverageCostBasisPerUnitInUSD))
					amount.Add(&amount, event.Amount)
					acquisitionDates = setUnion(acquisitionDates, event.AcquisitionDates)
				}

				fmt.Printf("%s %s\t%s\t%s\t$%s\t$%s\t$%s\t$%s\t$%s\t%s\n",
					format(&amount), currency,
					dateRange(setToStrings(acquisitionDates)), date,
					salesPriceSum.FloatString(2), costBasisSum.FloatString(2),
					new(big.Rat).Quo(&salesPriceSum, &amount).FloatString(2),
					new(big.Rat).Quo(&costBasisSum, &amount).FloatString(2),
					new(big.Rat).Sub(&salesPriceSum, &costBasisSum).FloatString(2), msg)
			}
		}
		fmt.Println()
	}

	fmt.Println("Balances:")
	for _, currency := range sortedCurrencies(db.Balances, false) {
		fmt.Println(" ", currency, db.Balances[currency].FloatString(2))
	}
}

func sortedCurrencies(balances map[string]*big.Rat, reverse bool) (rv []string) {
	rv = make([]string, 0, len(balances))
	for currency := range balances {
		rv = append(rv, currency)
	}
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(rv)))
	} else {
		sort.Strings(rv)
	}
	return rv
}

func sortedEvents(events map[string][]*TaxEvent, reverse bool) (rv []string) {
	rv = make([]string, 0, len(events))
	for key := range events {
		rv = append(rv, key)
	}
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(rv)))
	} else {
		sort.Strings(rv)
	}
	return rv
}

func must(a *big.Rat, ok bool) *big.Rat {
	if !ok {
		panic("failed")
	}
	return a
}

type HoldingsByCurrencyAndCostBasis struct {
	currency string
	holdings []*Holding
}

func (h HoldingsByCurrencyAndCostBasis) Len() int { return len(h.holdings) }

func (h HoldingsByCurrencyAndCostBasis) Swap(i, j int) {
	h.holdings[i], h.holdings[j] = h.holdings[j], h.holdings[i]
}

func (h HoldingsByCurrencyAndCostBasis) Less(i, j int) bool {
	if h.holdings[i].Currency != h.currency {
		return true
	}
	if h.holdings[j].Currency != h.currency {
		return false
	}
	return less(
		h.holdings[i].CostBasisPerUnitInUSD,
		h.holdings[j].CostBasisPerUnitInUSD)
}

func less(a, b *big.Rat) bool {
	return new(big.Rat).Sub(a, b).Sign() < 0
}

func format(a *big.Rat) string {
	rv := strings.TrimRightFunc(a.FloatString(8), func(r rune) bool {
		return r == '0'
	})
	return strings.TrimRightFunc(rv, func(r rune) bool {
		return r == '.'
	})
}

func isLongTerm(acquisitionDate, saleDate time.Time) bool {
	return saleDate.Sub(acquisitionDate) > 366*24*time.Hour
}

type HoldingsByCurrencyAndDate struct {
	currency string
	holdings []*Holding
}

func (h HoldingsByCurrencyAndDate) Len() int { return len(h.holdings) }

func (h HoldingsByCurrencyAndDate) Swap(i, j int) {
	h.holdings[i], h.holdings[j] = h.holdings[j], h.holdings[i]
}

func (h HoldingsByCurrencyAndDate) Less(i, j int) bool {
	if h.holdings[i].Currency != h.currency {
		return true
	}
	if h.holdings[j].Currency != h.currency {
		return false
	}
	return h.holdings[i].AcquisitionDate.Before(h.holdings[j].AcquisitionDate)
}

func setToStrings(set map[string]bool) []string {
	rv := make([]string, 0, len(set))
	for key := range set {
		rv = append(rv, key)
	}
	sort.Strings(rv)
	return rv
}

func setUnion(set1, set2 map[string]bool) map[string]bool {
	union := make(map[string]bool, len(set1)+len(set2))
	for key, val := range set1 {
		union[key] = val
	}
	for key, val := range set2 {
		union[key] = val
	}
	return union
}

func dateRange(dates []string) string {
	switch len(dates) {
	case 0:
		return ""
	case 1:
		return dates[0]
	case 2, 3, 4:
		return strings.Join(dates, ",")
	default:
		return fmt.Sprintf("%s - %s", dates[0], dates[len(dates)-1])
	}
}

func cleanAmount(amount string) string {
	return strings.ReplaceAll(strings.ReplaceAll(amount, "$", ""), ",", "")
}
