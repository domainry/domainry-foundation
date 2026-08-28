package decimalvalue

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecimalConfigNormalization(t *testing.T) {
	defaultConfig, err := NormalizeDecimalConfig(nil)
	if err != nil || defaultConfig != (DecimalConfig{Precision: 19, Scale: 2, RoundingMode: "half_even", CurrencyCode: "XXX"}) {
		t.Fatalf("default config=%+v err=%v", defaultConfig, err)
	}
	config, err := NormalizeDecimalConfig(map[string]any{"precision": json.Number("12"), "scale": 3, "rounding_mode": " HALF_UP ", "currency_code": " cny "})
	if err != nil || config != (DecimalConfig{Precision: 12, Scale: 3, RoundingMode: "half_up", CurrencyCode: "CNY"}) {
		t.Fatalf("normalized config=%+v err=%v", config, err)
	}

	cases := []struct {
		name string
		raw  map[string]any
		code string
	}{
		{"precision type", map[string]any{"precision": 1.5}, "backend.decimal.precision_invalid"},
		{"precision nan", map[string]any{"precision": math.NaN()}, "backend.decimal.precision_invalid"},
		{"precision infinity", map[string]any{"precision": math.Inf(1)}, "backend.decimal.precision_invalid"},
		{"precision float32 fraction", map[string]any{"precision": float32(1.5)}, "backend.decimal.precision_invalid"},
		{"precision float32 infinity", map[string]any{"precision": float32(math.Inf(1))}, "backend.decimal.precision_invalid"},
		{"precision low", map[string]any{"precision": 0}, "backend.decimal.precision_invalid"},
		{"precision high", map[string]any{"precision": 39}, "backend.decimal.precision_invalid"},
		{"scale type", map[string]any{"scale": true}, "backend.decimal.scale_invalid"},
		{"scale low", map[string]any{"scale": -1}, "backend.decimal.scale_invalid"},
		{"scale high", map[string]any{"precision": 2, "scale": 3}, "backend.decimal.scale_invalid"},
		{"rounding", map[string]any{"rounding_mode": "nearest"}, "backend.decimal.rounding_mode_invalid"},
		{"currency", map[string]any{"currency_code": "RMB" + "1"}, "backend.decimal.currency_code_invalid"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeDecimalConfig(testCase.raw)
			assertDecimalCode(t, err, testCase.code)
		})
	}
	for _, value := range []any{float64(2), float32(2), int8(2), int16(2), int32(2), int64(2), uint(2), uint8(2), uint16(2), uint32(2), uint64(2)} {
		config, err := NormalizeDecimalConfig(map[string]any{"precision": value})
		if err != nil || config.Precision != 2 {
			t.Errorf("precision %#v -> %+v err=%v", value, config, err)
		}
	}
}

func TestDecimalNormalizationAndArithmetic(t *testing.T) {
	config := DecimalConfig{Precision: 8, Scale: 2, RoundingMode: "half_even", CurrencyCode: "USD"}
	inputs := []struct {
		value any
		want  string
	}{
		{"0.1", "0.10"}, {json.Number("0.2"), "0.20"}, {1, "1.00"}, {int8(2), "2.00"}, {int16(3), "3.00"}, {int32(4), "4.00"}, {int64(5), "5.00"},
		{uint(6), "6.00"}, {uint8(7), "7.00"}, {uint16(8), "8.00"}, {uint32(9), "9.00"}, {uint64(10), "10.00"},
	}
	for _, input := range inputs {
		got, err := NormalizeDecimal(input.value, config)
		if err != nil || got != input.want {
			t.Errorf("normalize %#v=%q want=%q err=%v", input.value, got, input.want, err)
		}
	}
	for _, invalid := range []any{"", "bad", 1.2, math.NaN(), true, nil} {
		_, err := NormalizeDecimal(invalid, config)
		assertDecimalCode(t, err, "backend.decimal.value_invalid")
	}
	_, err := NormalizeDecimal("1234567.89", config)
	assertDecimalCode(t, err, "backend.decimal.precision_overflow")

	if got, err := AddDecimals("0.1", "0.2", config); err != nil || got != "0.30" {
		t.Fatalf("add=%q err=%v", got, err)
	}
	if got, err := SubtractDecimals("1", "0.25", config); err != nil || got != "0.75" {
		t.Fatalf("subtract=%q err=%v", got, err)
	}
	if got, err := CompareDecimals("9007199254740993.01", "9007199254740993.00", DecimalConfig{Precision: 19, Scale: 2, RoundingMode: "half_even", CurrencyCode: "USD"}); err != nil || got != 1 {
		t.Fatalf("compare=%d err=%v", got, err)
	}
	if _, err := CompareDecimals("1", "bad", config); err == nil {
		t.Fatal("invalid right comparison operand accepted")
	}
	if got, err := MultiplyDecimals("1.25", "2", config); err != nil || got != "2.50" {
		t.Fatalf("multiply=%q err=%v", got, err)
	}
	if got, err := DivideDecimals("1", "8", config); err != nil || got != "0.12" {
		t.Fatalf("divide=%q err=%v", got, err)
	}
	if got, err := PercentageDecimal("10", "15", config); err != nil || got != "1.50" {
		t.Fatalf("percentage=%q err=%v", got, err)
	}
	for _, operands := range [][2]string{{"bad", "1"}, {"1", "bad"}} {
		_, err := calculateDecimal(operands[0], operands[1], config, "add")
		assertDecimalCode(t, err, "backend.decimal.value_invalid")
	}
	_, err = DivideDecimals("1", "0", config)
	assertDecimalCode(t, err, "backend.decimal.divide_by_zero")
	_, err = calculateDecimal("1", "1", config, "unknown")
	assertDecimalCode(t, err, "backend.decimal.operator_invalid")
}

func TestDecimalRoundingModes(t *testing.T) {
	cases := map[string]string{
		"half_even": "1.22", "half_up": "1.23", "down": "1.22", "up": "1.23", "floor": "1.22", "ceiling": "1.23",
	}
	value := decimal.RequireFromString("1.225")
	for mode, want := range cases {
		if got := roundDecimal(value, 2, mode).StringFixed(2); got != want {
			t.Errorf("round %s=%s want=%s", mode, got, want)
		}
	}
}

func TestDecimalInputAndErrors(t *testing.T) {
	if _, ok := decimalInput(struct{}{}); ok {
		t.Fatal("unsupported input accepted")
	}
	if _, ok := decimalInteger("not-integer"); ok {
		t.Fatal("invalid integer accepted")
	}
	err := decimalError("decimal.failure")
	var typed *DecimalError
	if !errors.As(err, &typed) || typed.Error() != "decimal.failure" {
		t.Fatalf("error=%#v", err)
	}
	if got := decimalRoundingModes(); !reflect.DeepEqual(got, map[string]bool{"half_even": true, "half_up": true, "down": true, "up": true, "floor": true, "ceiling": true}) {
		t.Fatalf("rounding modes=%v", got)
	}
}

func TestDecimalHundredThousandPaymentRefundBalanceInvariant(t *testing.T) {
	config := DecimalConfig{Precision: 19, Scale: 2, RoundingMode: "half_even", CurrencyCode: "CNY"}
	random := rand.New(rand.NewSource(20260721))
	balance := "0.00"
	var paidMinorUnits, refundedMinorUnits int64
	for index := 0; index < 100_000; index++ {
		paid := int64(random.Intn(1_000_000) + 1)
		refunded := int64(random.Intn(int(paid) + 1))
		var err error
		balance, err = AddDecimals(balance, decimalMinorUnits(paid), config)
		if err != nil {
			t.Fatalf("payment %d: %v", index, err)
		}
		balance, err = SubtractDecimals(balance, decimalMinorUnits(refunded), config)
		if err != nil {
			t.Fatalf("refund %d: %v", index, err)
		}
		paidMinorUnits += paid
		refundedMinorUnits += refunded
	}
	want := decimalMinorUnits(paidMinorUnits - refundedMinorUnits)
	if balance != want {
		t.Fatalf("payment-refund-balance invariant: paid=%s refunded=%s balance=%s want=%s", decimalMinorUnits(paidMinorUnits), decimalMinorUnits(refundedMinorUnits), balance, want)
	}
}

func decimalMinorUnits(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

func assertDecimalCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *DecimalError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%#v want=%s", err, code)
	}
}
