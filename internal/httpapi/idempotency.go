package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

func hashTripData(data api.TripData) ([]byte, error) {
	canonical, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal trip data: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}

func (s *Server) replayTrip(ctx context.Context, key uuid.UUID, requestHash []byte) (domain.Trip, error) {
	record, err := s.keys.Get(ctx, key)
	if err != nil {
		return domain.Trip{}, err
	}
	if !bytes.Equal(record.RequestHash, requestHash) {
		return domain.Trip{}, domain.ErrIdempotencyConflict
	}
	return s.trips.GetByID(ctx, record.TripID)
}
