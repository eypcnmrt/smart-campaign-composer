package worker

// Observer is told what the worker does, so a metrics package can count it without the worker importing one. Every method is
// called from the worker's goroutines and must be quick and safe for concurrent use.
type Observer interface {
	// Attempt is called once per recorded outcome of a send: result is sent, retried, failed, skipped or refunded (the attempt
	// ran into a known outage and was given back), or lease_lost / record_error when the outcome could not be written.
	Attempt(channel, result string)
	// Circuit reports the state of a channel's circuit (closed, open or half_open) while the worker serves the channel.
	Circuit(channel, state string)
	// LockHeld says whether this worker currently holds the channel's advisory lock, that is, whether it serves the channel.
	LockHeld(channel string, held bool)
	// HeartbeatError is called when writing the channel's heartbeat failed.
	HeartbeatError(channel string)
	// MaintenanceError is called when one task of a maintenance pass failed (a Task constant). While it keeps happening,
	// expired leases are not taken back and finished campaigns stay "sending", although the heartbeat says the worker is fine.
	MaintenanceError(task string)
}

// The tasks of the maintenance loop, as reported to Observer.MaintenanceError.
const (
	TaskRecoverLeases     = "recover_leases"
	TaskCompleteCampaigns = "complete_campaigns"
)

// MaintenanceTasks lists every task MaintenanceError can name (a metrics package starts each of them at zero).
func MaintenanceTasks() []string { return []string{TaskRecoverLeases, TaskCompleteCampaigns} }

type nopObserver struct{}

func (nopObserver) Attempt(string, string)  {}
func (nopObserver) Circuit(string, string)  {}
func (nopObserver) LockHeld(string, bool)   {}
func (nopObserver) HeartbeatError(string)   {}
func (nopObserver) MaintenanceError(string) {}
