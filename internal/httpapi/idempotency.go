package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/BeganovR/Golang-BeganovR/api"
)

func hashTripData(data api.TripData) ([]byte, error) {
	canonical, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal trip data: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}
