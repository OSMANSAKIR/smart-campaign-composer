package campaign

// StatusScheduled is a campaign approved with send_at: its messages are enqueued but nothing is claimed
// until the worker's scheduler moves it to `sending` once the database clock passes scheduled_at
// (docs/contracts/api.md §9.6; the scheduler lives in internal/delivery).
const StatusScheduled = "scheduled"
