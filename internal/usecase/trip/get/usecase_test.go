package get_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/get"
)

func TestExecute(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, wantErr := range []error{nil, trip.ErrTripNotFound, storageErr} {
		name := "success"
		if wantErr != nil {
			name = wantErr.Error()
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repo := NewMockTripRepository(gomock.NewController(t))
			id := uuid.New()
			want := trip.Trip{ID: id, UserID: uuid.New(), DriverID: uuid.New(), Status: trip.TripStatusActive}
			repo.EXPECT().GetByID(ctx, id).Return(want, wantErr)
			got, err := get.New(repo).Execute(ctx, id)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, wantErr)
			}
			if wantErr != nil {
				want = trip.Trip{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Execute() = %+v, want %+v", got, want)
			}
		})
	}
}
