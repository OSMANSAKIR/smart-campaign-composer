package campaign

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"smartcampaign/internal/store/storetest"
)

func TestTransition_ConcurrentOnlyOneWins_RFLOW3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, "INSERT INTO campaigns (brief, status) VALUES ('x', 'content_ready') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Transition(ctx, pool, id, []string{StatusContentReady}, StatusSending)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrInvalidState):
				conflicts.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != 19 {
		t.Fatalf("wins=%d conflicts=%d", wins.Load(), conflicts.Load())
	}
}
