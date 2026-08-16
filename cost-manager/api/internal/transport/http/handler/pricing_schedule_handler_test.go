package handler

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/transport/http/dto"
	"github.com/gin-gonic/gin"
)

func TestPricingScheduleFirstVersionRequiresExplicitZeroOCC(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{name: "explicit zero", body: `{"expected_latest_version":0,"effective_from":"2026-08-16T00:00:00Z","change_reason":"first operator price","brackets":[{"range_start_quantity":"0","price_numerator_micro_units":"1","price_denominator_quantity":"1"}]}`, want: true},
		{name: "missing OCC", body: `{"effective_from":"2026-08-16T00:00:00Z","change_reason":"first operator price","brackets":[{"range_start_quantity":"0","price_numerator_micro_units":"1","price_denominator_quantity":"1"}]}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(nil)
			ctx.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			var request dto.CreatePricingScheduleVersionRequest
			err := ctx.ShouldBindJSON(&request)
			if (err == nil) != test.want {
				t.Fatalf("binding success = %v, want %v (err=%v)", err == nil, test.want, err)
			}
		})
	}
}

func TestParsePricingBracketRequestPreservesInt64Boundary(t *testing.T) {
	end := "9223372036854775807"
	parsed, err := parsePricingBracketRequest(dto.CreateScalarBracketRequest{
		RangeStartQuantity:       "0",
		RangeEndQuantity:         &end,
		PriceNumeratorMicroUnits: "9223372036854775807",
		PriceDenominatorQuantity: "1",
	})
	if err != nil {
		t.Fatalf("parse pricing bracket: %v", err)
	}
	if parsed.RangeEndQuantity == nil || *parsed.RangeEndQuantity != int64(9223372036854775807) || parsed.PriceNumeratorMicroUnits != int64(9223372036854775807) {
		t.Fatal("expected exact int64 max without JSON number rounding")
	}

	overflow := "9223372036854775808"
	if _, err := parsePricingBracketRequest(dto.CreateScalarBracketRequest{RangeStartQuantity: overflow, PriceNumeratorMicroUnits: "1", PriceDenominatorQuantity: "1"}); err == nil {
		t.Fatal("expected out-of-range decimal string to be rejected")
	}
}

func TestPricingScheduleVersionResponseUsesStringsAndUTC(t *testing.T) {
	end := int64(9223372036854775807)
	effectiveTo := time.Date(2026, 8, 15, 13, 30, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	response := pricingScheduleVersionResponse(entity.PricingScheduleVersionPublished{
		EffectiveFrom: effectiveTo,
		EffectiveTo:   &effectiveTo,
	}, []entity.PricingScheduleVersionPublishBracket{{
		RangeStartQuantity:       0,
		RangeEndQuantity:         &end,
		PriceNumeratorMicroUnits: end,
		PriceDenominatorQuantity: 1,
	}})
	brackets := response["brackets"].([]gin.H)
	if got := brackets[0]["price_numerator_micro_units"]; got != "9223372036854775807" {
		t.Fatalf("price numerator = %#v, want exact decimal string", got)
	}
	if got := brackets[0]["range_end_quantity"]; got != "9223372036854775807" {
		t.Fatalf("range end = %#v, want exact decimal string", got)
	}
	if got := response["effective_from"]; got != "2026-08-15T06:30:00Z" {
		t.Fatalf("effective_from = %#v, want UTC Z timestamp", got)
	}
}
