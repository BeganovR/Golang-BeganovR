package request_test

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
)

const validBody = `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c","driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5","start_point":{"latitude":59.9,"longitude":30.3},"end_point":{"latitude":60,"longitude":31},"price":1450}`

func TestTripData(t *testing.T) {
	validator, err := request.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		body  string
		price int64
		valid bool
	}{
		{name: "valid", body: validBody, price: 1450, valid: true},
		{name: "free", body: strings.Replace(validBody, `"price":1450`, `"price":0`, 1), valid: true},
		{name: "maximum int64", body: strings.Replace(validBody, `"price":1450`, `"price":9223372036854775807`, 1), price: math.MaxInt64, valid: true},
		{name: "empty"},
		{name: "malformed", body: `{"private_value":"sensitive",`},
		{name: "trailing data", body: validBody + `{}`},
		{name: "null", body: `null`},
		{name: "array", body: `[]`},
		{name: "unknown field", body: strings.TrimSuffix(validBody, "}") + `,"private_value":"sensitive"}`},
		{name: "unknown coordinate", body: strings.Replace(validBody, `"latitude":59.9`, `"private_value":"sensitive","latitude":59.9`, 1)},
		{name: "missing required", body: `{"price":0}`},
		{name: "missing longitude", body: strings.Replace(validBody, `,"longitude":30.3`, "", 1)},
		{name: "null coordinates", body: strings.Replace(validBody, `{"latitude":59.9,"longitude":30.3}`, "null", 1)},
		{name: "null latitude", body: strings.Replace(validBody, `"latitude":59.9`, `"latitude":null`, 1)},
		{name: "latitude out of range", body: strings.Replace(validBody, `"latitude":59.9`, `"latitude":91`, 1)},
		{name: "longitude out of range", body: strings.Replace(validBody, `"longitude":30.3`, `"longitude":-181`, 1)},
		{name: "malformed UUID", body: strings.Replace(validBody, "5cb72c04-7650-45c9-a79b-bcdba0631e0c", "private_value", 1)},
		{name: "compact UUID", body: strings.Replace(validBody, "5cb72c04-7650-45c9-a79b-bcdba0631e0c", "5cb72c04765045c9a79bbcdba0631e0c", 1)},
		{name: "zero UUID", body: strings.Replace(validBody, "5cb72c04-7650-45c9-a79b-bcdba0631e0c", "00000000-0000-0000-0000-000000000000", 1)},
		{name: "null UUID", body: strings.Replace(validBody, `"5cb72c04-7650-45c9-a79b-bcdba0631e0c"`, "null", 1)},
		{name: "negative price", body: strings.Replace(validBody, `"price":1450`, `"price":-1`, 1)},
		{name: "fractional price", body: strings.Replace(validBody, `"price":1450`, `"price":1.5`, 1)},
		{name: "price overflow", body: strings.Replace(validBody, `"price":1450`, `"price":9223372036854775808`, 1)},
		{name: "huge number", body: strings.Replace(validBody, `"price":1450`, `"price":1e309`, 1)},
		{name: "null price", body: strings.Replace(validBody, `"price":1450`, `"price":null`, 1)},
		{name: "price string", body: strings.Replace(validBody, `"price":1450`, `"price":"private_value"`, 1)},
		{name: "oversized body", body: strings.Repeat(" ", 1<<20) + validBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tt.body))
			data, err := validator.TripData(httptest.NewRecorder(), r)
			if tt.valid {
				if err != nil {
					t.Fatal(err)
				}
				if data.Price != tt.price || data.StartPoint.Latitude != 59.9 || data.EndPoint.Longitude != 31 {
					t.Fatalf("incorrect parsed data: %+v", data)
				}
				return
			}
			if !errors.Is(err, domainerror.ErrInvalidRequest) {
				t.Fatalf("got error=%v", err)
			}
			if strings.Contains(err.Error(), "private_value") || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("request data leaked into error: %s", err)
			}
		})
	}
}

func TestUUID(t *testing.T) {
	for _, value := range []string{"5cb72c04-7650-45c9-a79b-bcdba0631e0c", "5CB72C04-7650-45C9-A79B-BCDBA0631E0C"} {
		if !request.UUID(value) {
			t.Errorf("rejected canonical UUID %s", value)
		}
	}
	for _, value := range []string{"", "not-a-uuid", "5cb72c04765045c9a79bbcdba0631e0c", "{5cb72c04-7650-45c9-a79b-bcdba0631e0c}", "urn:uuid:5cb72c04-7650-45c9-a79b-bcdba0631e0c"} {
		if request.UUID(value) {
			t.Errorf("accepted noncanonical UUID %s", value)
		}
	}
}

func TestValidationDetails(t *testing.T) {
	t.Parallel()
	const priceField = "price"
	validator, err := request.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		body   string
		field  string
		reason string
	}{
		{name: "malformed JSON", body: `{"private_value":"sensitive",`, field: "body", reason: "valid JSON"},
		{name: "missing coordinate", body: strings.Replace(validBody, `,"longitude":30.3`, "", 1), field: "start_point.longitude", reason: "missing"},
		{name: "invalid coordinate", body: strings.Replace(validBody, `"latitude":59.9`, `"latitude":91`, 1), field: "start_point.latitude", reason: "90"},
		{name: "negative price", body: strings.Replace(validBody, `"price":1450`, `"price":-1`, 1), field: priceField, reason: "0"},
		{name: "wrong type", body: strings.Replace(validBody, `"price":1450`, `"price":"sensitive"`, 1), field: priceField, reason: "integer"},
		{name: "overflow", body: strings.Replace(validBody, `"price":1450`, `"price":9223372036854775808`, 1), field: priceField, reason: "declared field type"},
		{name: "invalid UUID", body: strings.Replace(validBody, "5cb72c04-7650-45c9-a79b-bcdba0631e0c", "sensitive", 1), field: "user_id", reason: "UUID"},
		{name: "zero UUID", body: strings.Replace(validBody, "8860b315-ec86-42eb-a17c-7c163d721ff5", "00000000-0000-0000-0000-000000000000", 1), field: "driver_id", reason: "must not be empty"},
		{name: "unknown field", body: strings.TrimSuffix(validBody, "}") + `,"private_value":"sensitive"}`, field: "body", reason: "unknown fields"},
		{name: "unknown nested field", body: strings.Replace(validBody, `"latitude":59.9`, `"private_value":"sensitive","latitude":59.9`, 1), field: "start_point", reason: "unknown fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tc.body))
			_, err := validator.TripData(httptest.NewRecorder(), r)
			status, body := domainerror.Problem(err)
			if status != http.StatusBadRequest || body.Detail == nil {
				t.Fatalf("validation problem = %d, %+v", status, body)
			}
			if !strings.HasPrefix(*body.Detail, tc.field+": ") || !strings.Contains(*body.Detail, tc.reason) {
				t.Errorf("detail = %q, want field %q and reason %q", *body.Detail, tc.field, tc.reason)
			}
			if strings.Contains(*body.Detail, "sensitive") || strings.Contains(*body.Detail, "private_value") {
				t.Fatalf("request contents leaked: %q", *body.Detail)
			}
		})
	}
}
