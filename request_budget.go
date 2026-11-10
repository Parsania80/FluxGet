package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The queue owner configures this broker. Workers only acquire/release leases.
// A single lock grants global and origin capacity together; no partial grants.
type requestPolicy struct {
	Mode      string `json:"mode"`
	Total     int    `json:"total"`
	PerOrigin int    `json:"per_origin"`
}

func (p requestPolicy) validate() error {
	if (p.Mode != "automatic" && p.Mode != "manual") || p.Total < 1 || p.Total > 64 || p.PerOrigin < 1 || p.PerOrigin > 16 {
		return errors.New("choose Automatic or Manual, 1–64 total requests and 1–16 per server")
	}
	return nil
}
func defaultRequestPolicy() requestPolicy {
	return requestPolicy{Mode: "automatic", Total: 8, PerOrigin: 4}
}

type budgetWaiter struct {
	ctx    context.Context
	job    int
	origin string
	ready  chan *requestLease
}
type budgetSnapshot struct {
	Policy     requestPolicy `json:"policy"`
	Active     int           `json:"active"`
	Waiting    int           `json:"waiting"`
	FileTarget int           `json:"file_target,omitempty"`
	Draining   bool          `json:"draining,omitempty"`
	Status     string        `json:"status,omitempty"`
	Reason     string        `json:"reason,omitempty"`
}
type jobBudgetSnapshot struct {
	Active   int  `json:"active"`
	Waiting  int  `json:"waiting"`
	Cooldown bool `json:"cooldown,omitempty"`
}
type requestBudget struct {
	mu         sync.Mutex
	policy     requestPolicy
	active     int
	origins    map[string]int
	jobs       map[int]int
	pending    []*budgetWaiter
	cooldowns  map[string]time.Time
	feedback   map[string]uint64
	blocked    map[string]bool
	originCaps map[string]int
	recoverAt  map[string]time.Time
	timer      *time.Timer
	closed     bool
}

func newRequestBudget(p requestPolicy) *requestBudget {
	return &requestBudget{policy: p, origins: map[string]int{}, jobs: map[int]int{}, cooldowns: map[string]time.Time{}, feedback: map[string]uint64{}, blocked: map[string]bool{}, originCaps: map[string]int{}, recoverAt: map[string]time.Time{}}
}
func (b *requestBudget) configure(p requestPolicy) error {
	if err := p.validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.policy.Mode != p.Mode {
		b.cooldowns = map[string]time.Time{}
		b.blocked = map[string]bool{}
		b.originCaps = map[string]int{}
		b.recoverAt = map[string]time.Time{}
		b.feedback = map[string]uint64{}
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
	}
	b.policy = p
	b.grantLocked()
	return nil
}
func (b *requestBudget) snapshot() (budgetSnapshot, map[int]jobBudgetSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := budgetSnapshot{Policy: b.policy, Active: b.active}
	jobs := map[int]jobBudgetSnapshot{}
	for id, n := range b.jobs {
		jobs[id] = jobBudgetSnapshot{Active: n}
	}
	for _, p := range b.pending {
		if p.ctx.Err() == nil {
			j := jobs[p.job]
			j.Waiting++
			j.Cooldown = j.Cooldown || b.originCoolingLocked(p.origin, time.Now())
			jobs[p.job] = j
			snapshot.Waiting++
		}
	}
	return snapshot, jobs
}
func (b *requestBudget) acquire(ctx context.Context, job int, origin string) (*requestLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &budgetWaiter{ctx: ctx, job: job, origin: origin, ready: make(chan *requestLease, 1)}
	b.mu.Lock()
	// At most 64 jobs x 16 parts, plus metadata; never allow an unbounded queue.
	if len(b.pending) >= 2048 {
		b.mu.Unlock()
		return nil, errors.New("request admission queue is full")
	}
	b.pending = append(b.pending, w)
	b.grantLocked()
	b.mu.Unlock()
	select {
	case lease := <-w.ready:
		if err := ctx.Err(); err != nil {
			lease.release()
			return nil, err
		}
		return lease, nil
	case <-ctx.Done():
		b.mu.Lock()
		for i, p := range b.pending {
			if p == w {
				b.pending = append(b.pending[:i], b.pending[i+1:]...)
				break
			}
		}
		b.grantLocked()
		b.mu.Unlock()
		select {
		case lease := <-w.ready:
			lease.release()
		default:
		}
		return nil, ctx.Err()
	}
}
func (b *requestBudget) grantLocked() {
	if b.closed {
		return
	}
	now := time.Now()
	for {
		// Prefer a waiting job with fewer current grants, FIFO for ties. Blocked
		// origins do not hold global slots or prevent unrelated work from starting.
		best := -1
		for i, w := range b.pending {
			if w.ctx.Err() != nil {
				continue
			}
			if b.policy.Mode == "automatic" && (b.active >= b.policy.Total || b.origins[w.origin] >= b.originLimitLocked(w.origin) || b.originCoolingLocked(w.origin, now)) {
				continue
			}
			if best < 0 || b.jobs[w.job] < b.jobs[b.pending[best].job] {
				best = i
			}
		}
		if best < 0 {
			return
		}
		w := b.pending[best]
		b.pending = append(b.pending[:best], b.pending[best+1:]...)
		b.active++
		b.origins[w.origin]++
		b.jobs[w.job]++
		w.ready <- &requestLease{budget: b, job: w.job, origin: w.origin}
	}
}

type requestLease struct {
	budget *requestBudget
	job    int
	origin string
	once   sync.Once
}

func (l *requestLease) release() {
	l.once.Do(func() {
		b := l.budget
		b.mu.Lock()
		defer b.mu.Unlock()
		b.active--
		b.origins[l.origin]--
		b.jobs[l.job]--
		if b.origins[l.origin] == 0 {
			delete(b.origins, l.origin)
		}
		if b.jobs[l.job] == 0 {
			delete(b.jobs, l.job)
		}
		b.grantLocked()
	})
}
func requestOrigin(u *url.URL) (string, error) {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("request requires an HTTP or HTTPS origin")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", errors.New("invalid HTTP origin port")
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.FormatUint(number, 10)), nil
}

type budgetContextKey struct{}
type requestSegmentKey struct{}
type requestTransport struct {
	base    http.RoundTripper
	budget  *requestBudget
	job     int
	metrics *transferMeasurements
}

func (t *requestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := requestOrigin(req.URL)
	if err != nil {
		return nil, err
	}
	t.metrics.origin.Store(&origin)
	part, _ := req.Context().Value(requestSegmentKey{}).(*segmentPart)
	part.setState(segmentWaiting)
	started := time.Now()
	lease, err := t.budget.acquire(req.Context(), t.job, origin)
	t.metrics.addDuration(metricBudgetWait, time.Since(started))
	if err != nil {
		return nil, err
	}
	part.setState(segmentConnecting)
	t.metrics.requests.Add(1)
	started = time.Now()
	response, err := t.base.RoundTrip(req)
	t.metrics.addDuration(metricHeaderWait, time.Since(started))
	if err != nil {
		lease.release()
		if req.Context().Err() == context.Canceled {
			err = context.Canceled
		} else {
			t.metrics.failures.Add(1)
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				t.metrics.timeouts.Add(1)
			}
		}
		return nil, err
	}
	if response.StatusCode == 429 || response.StatusCode == 503 {
		status, _ := transientStatus(response).(*httpStatusError)
		if status != nil {
			t.budget.serverCooldown(origin, max(time.Second, status.retryAfter), status.tooLong)
		}
	}
	if response.StatusCode >= 400 {
		t.metrics.httpErrors.Add(1)
	}
	body := &measuredRequestBody{ReadCloser: response.Body, lease: lease, metrics: t.metrics, ctx: req.Context()}
	body.stop = context.AfterFunc(req.Context(), func() { body.close() })
	response.Body = body
	return response, nil
}
func (t *requestTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type measuredRequestBody struct {
	io.ReadCloser
	lease   *requestLease
	metrics *transferMeasurements
	stop    func() bool
	once    sync.Once
	err     error
	ctx     context.Context
}

func (b *measuredRequestBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := b.ReadCloser.Read(p)
	b.metrics.addDuration(metricNetworkRead, time.Since(start))
	b.metrics.networkBytes.Add(int64(n))
	if err != nil {
		// A canceled HTTP/2 stream can report a closed body instead of cancellation.
		if b.ctx != nil && b.ctx.Err() == context.Canceled {
			err = context.Canceled
		}
		if !errors.Is(err, io.EOF) && (b.ctx == nil || b.ctx.Err() != context.Canceled) {
			b.metrics.failures.Add(1)
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				b.metrics.timeouts.Add(1)
			}
		}
		b.lease.release()
	}
	return n, err
}
func (b *measuredRequestBody) close() {
	b.once.Do(func() { b.err = b.ReadCloser.Close(); b.lease.release() })
}
func (b *measuredRequestBody) Close() error {
	b.close()
	// The callback may itself be calling Close. Stopping it never waits.
	if b.stop != nil {
		b.stop()
	}
	return b.err
}

// Server feedback is recorded before releasing the response lease, preventing
// sibling jobs from bypassing the cooldown. The queue consumes reduction events.
func (b *requestBudget) serverCooldown(origin string, delay time.Duration, blocked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.policy.Mode != "automatic" {
		return
	}
	// Expired timed cooldowns are removed; manual stops last until user retry.
	for key, until := range b.cooldowns {
		if !until.After(time.Now()) {
			delete(b.cooldowns, key)
		}
	}
	until := time.Now().Add(delay)
	if until.After(b.cooldowns[origin]) {
		b.cooldowns[origin] = until
	}
	if blocked {
		b.blocked[origin] = true
	}
	b.feedback[origin]++
	b.scheduleWakeLocked()
}
func (b *requestBudget) originCoolingLocked(origin string, now time.Time) bool {
	return b.blocked[origin] || b.cooldowns[origin].After(now)
}
func (b *requestBudget) canStart(origin string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return origin != "" && (b.policy.Mode != "automatic" || (!b.originCoolingLocked(origin, now) && b.active < b.policy.Total && b.origins[origin] < b.originLimitLocked(origin)))
}
func (b *requestBudget) originCooling(origin string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.policy.Mode == "automatic" && b.originCoolingLocked(origin, now)
}
func (b *requestBudget) takeFeedback() map[string]uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	feedback := b.feedback
	b.feedback = map[string]uint64{}
	return feedback
}
func (b *requestBudget) scheduleWakeLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	var next time.Time
	now := time.Now()
	for origin, until := range b.cooldowns {
		if !until.After(now) {
			delete(b.cooldowns, origin)
			continue
		}
		if b.blocked[origin] {
			continue
		}
		if next.IsZero() || until.Before(next) {
			next = until
		}
	}
	if !next.IsZero() {
		b.timer = time.AfterFunc(time.Until(next), func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.closed {
				return
			}
			b.grantLocked()
			b.scheduleWakeLocked()
		})
	}
}
func (b *requestBudget) retryOrigin(origin string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blocked, origin)
	delete(b.originCaps, origin)
	delete(b.recoverAt, origin)
	delete(b.cooldowns, origin)
	b.grantLocked()
	b.scheduleWakeLocked()
}
func (b *requestBudget) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
	}
}

// Removed jobs must not leave an ever-growing origin history. Keep live leases,
// pending redirect hops, and origins still referenced by queue entries.
func (b *requestBudget) pruneOrigins(keep map[string]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.pending {
		keep[w.origin] = true
	}
	for origin := range b.blocked {
		if _, ok := b.recoverAt[origin]; !ok {
			b.recoverAt[origin] = time.Time{}
		}
	}
	for origin := range b.recoverAt {
		if !keep[origin] && b.origins[origin] == 0 {
			delete(b.blocked, origin)
			delete(b.originCaps, origin)
			delete(b.recoverAt, origin)
			delete(b.cooldowns, origin)
			delete(b.feedback, origin)
		}
	}
	b.scheduleWakeLocked()
}

func (b *requestBudget) originLimitLocked(origin string) int {
	limit := b.policy.PerOrigin
	if learned := b.originCaps[origin]; learned > 0 {
		limit = min(limit, learned)
	}
	return limit
}

// The queue reduces aggregate origin capacity as well as individual worker
// targets, so several one-worker jobs cannot bypass a server's backoff.
func (b *requestBudget) backOffOrigin(origin string, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.originCaps[origin] = max(1, min(b.originLimitLocked(origin), max(1, b.origins[origin]))/2)
	b.recoverAt[origin] = now.Add(time.Minute)
}

// Recovery invalidates the queue's comparison baseline. Restore at most one
// origin per tick after a quiet minute; hard settings and cooldowns still win.
func (b *requestBudget) recoverOrigin(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	selected := ""
	for origin, at := range b.recoverAt {
		if at.IsZero() || now.Before(at) || b.originCoolingLocked(origin, time.Now()) {
			continue
		}
		if selected == "" || origin < selected {
			selected = origin
		}
	}
	if selected == "" {
		return false
	}
	delete(b.originCaps, selected)
	delete(b.recoverAt, selected)
	b.grantLocked()
	return true
}
