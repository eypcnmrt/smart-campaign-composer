// Package seed fills the database with deterministic demo data: users spread over the
// canonical cities and languages, with cart_add and purchase events over the last 90 days.
//
// Every random draw comes from a generator with a fixed seed, so two runs produce the same
// users. Rows are inserted with ON CONFLICT DO NOTHING on the unique e-mail and phone, and
// events are written only for users that were actually inserted, so running the seed again
// adds nothing.
//
// The contact data is fake on purpose: e-mails use example.com (reserved by RFC 2606) and
// phone numbers have the form +90 000 followed by seven digits. No Turkish number starts with
// 0 after the country code, so none of them can reach a real person.
package seed

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/segment"
)

// Defaults for a normal run.
const (
	DefaultUsers   = 5000
	DefaultRNGSeed = 20251113
	// eventWindow is how far back events are spread (docs/PLAN.md section 2, assumption 12).
	eventWindow = 90 * 24 * time.Hour
)

// seedLockKey serializes concurrent seed runs.
const seedLockKey int64 = 0x5343435f5345 // "SCC_SE"

// Options configures a run. Zero fields take the defaults.
type Options struct {
	Users   int
	RNGSeed uint64
	// Now is the end of the event window; the zero value means time.Now().
	Now time.Time
}

// Result says how many rows a run added.
type Result struct {
	UsersInserted  int
	EventsInserted int
}

type user struct {
	firstName    string
	email, phone string
	city, lang   string
	orders       int
	spentKurus   int64
	smsConsent   bool
	emailConsent bool
	events       []event
}

type event struct {
	kind string
	at   time.Time
}

var firstNames = []string{
	"Ahmet", "Mehmet", "Mustafa", "Ali", "Hasan", "Hüseyin", "İbrahim", "Emre", "Burak", "Can",
	"Ayşe", "Fatma", "Emine", "Hatice", "Zeynep", "Elif", "Merve", "Büşra", "Selin", "Deniz",
	"Cem", "Kerem", "Onur", "Murat", "Serkan", "Esra", "Gizem", "Ceren", "Yusuf", "Ece",
}

// cityWeights makes the first cities (the big ones in segment.Cities) more common; the
// remaining ones share the rest evenly.
var cityWeights = []int{30, 14, 10, 8, 6, 5, 5, 5, 4, 4, 4, 5}

// generate builds the users for the given options without touching the database.
func generate(opts Options) []user {
	opts = withDefaults(opts)
	rng := rand.New(rand.NewPCG(opts.RNGSeed, opts.RNGSeed^0x9e3779b97f4a7c15))
	cities := segment.Cities()
	langs := segment.Languages()

	users := make([]user, 0, opts.Users)
	for i := 1; i <= opts.Users; i++ {
		u := user{
			firstName: firstNames[rng.IntN(len(firstNames))],
			email:     fmt.Sprintf("user%05d@example.com", i),
			phone:     fmt.Sprintf("+90000%07d", i),
			city:      cities[pick(rng, cityWeights, len(cities))],
			lang:      langs[0],
			// Different consent rates per channel: SMS is opt-in less often than e-mail.
			smsConsent:   rng.IntN(100) < 70,
			emailConsent: rng.IntN(100) < 85,
		}
		if len(langs) > 1 && rng.IntN(100) < 15 {
			u.lang = langs[1]
		}

		carts, purchases, older := rng.IntN(7), rng.IntN(4), rng.IntN(6)
		for range carts {
			u.events = append(u.events, event{"cart_add", opts.Now.Add(-time.Duration(rng.Int64N(int64(eventWindow/time.Second))) * time.Second)})
		}
		for range purchases {
			u.events = append(u.events, event{"purchase", opts.Now.Add(-time.Duration(rng.Int64N(int64(eventWindow/time.Second))) * time.Second)})
		}
		// total_orders also counts orders older than the event window.
		u.orders = purchases + older
		for range u.orders {
			u.spentKurus += 5000 + rng.Int64N(250000) // 50.00 - 2550.00 TL per order
		}
		users = append(users, u)
	}
	return users
}

// pick returns an index drawn with the given weights; indexes past the weights get weight 4.
func pick(rng *rand.Rand, weights []int, n int) int {
	w := func(i int) int {
		if i < len(weights) {
			return weights[i]
		}
		return 4
	}
	total := 0
	for i := range n {
		total += w(i)
	}
	r := rng.IntN(total)
	for i := range n {
		if r < w(i) {
			return i
		}
		r -= w(i)
	}
	return n - 1
}

func withDefaults(o Options) Options {
	if o.Users <= 0 {
		o.Users = DefaultUsers
	}
	if o.RNGSeed == 0 {
		o.RNGSeed = DefaultRNGSeed
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	o.Now = o.Now.UTC().Truncate(time.Second)
	return o
}

// Run inserts the generated users and their events in one transaction.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options) (res Result, err error) {
	users := generate(opts)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()

	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, seedLockKey); err != nil {
		return Result{}, fmt.Errorf("taking seed lock: %w", err)
	}

	const insertUser = `INSERT INTO users
		(first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING
		RETURNING id`
	batch := &pgx.Batch{}
	for _, u := range users {
		batch.Queue(insertUser, u.firstName, u.email, u.phone, u.city, u.lang, u.orders, u.spentKurus, u.smsConsent, u.emailConsent)
	}
	results := tx.SendBatch(ctx, batch)
	var rows [][]any
	for _, u := range users {
		var id int64
		scanErr := results.QueryRow().Scan(&id)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			continue // the user already exists
		}
		if scanErr != nil {
			_ = results.Close()
			return Result{}, fmt.Errorf("inserting user %s: %w", u.email, scanErr)
		}
		res.UsersInserted++
		for _, e := range u.events {
			rows = append(rows, []any{id, e.kind, e.at})
		}
	}
	if err = results.Close(); err != nil {
		return Result{}, fmt.Errorf("closing user batch: %w", err)
	}

	n, err := tx.CopyFrom(ctx, pgx.Identifier{"events"}, []string{"user_id", "event_type", "occurred_at"}, pgx.CopyFromRows(rows))
	if err != nil {
		return Result{}, fmt.Errorf("inserting events: %w", err)
	}
	res.EventsInserted = int(n)

	if err = tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}
