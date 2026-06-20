package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core/push"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"github.com/nats-io/nats.go"
)

const (
	natsHeaderSchemaVersion = "Jmap2nats-Schema-Version"
	wireSchemaVersion       = "1"
)

type envelope struct {
	AccountID jmap.ID
	Email     *email.Email
	Parts     map[jmap.ID]*partResult
}

type partResult struct {
	ObjectKey string
	Skipped   bool
	Error     string
}

type partView struct {
	PartID      string          `json:"partId,omitempty"`
	BlobID      jmap.ID         `json:"blobId,omitempty"`
	Size        uint64          `json:"size,omitempty"`
	Headers     []*email.Header `json:"headers,omitempty"`
	Name        string          `json:"name,omitempty"`
	Type        string          `json:"type,omitempty"`
	Charset     string          `json:"charset,omitempty"`
	Disposition string          `json:"disposition,omitempty"`
	CID         string          `json:"cid,omitempty"`
	Language    []string        `json:"language,omitempty"`
	Location    string          `json:"location,omitempty"`
	SubParts    []*partView     `json:"subParts,omitempty"`

	ObjectKey *string `json:"objectKey,omitempty"`
	Skipped   bool    `json:"skipped,omitempty"`
	Error     string  `json:"error,omitempty"`
}

type messageView struct {
	ID            jmap.ID          `json:"id"`
	AccountID     jmap.ID          `json:"accountId"`
	BlobID        jmap.ID          `json:"blobId,omitempty"`
	ThreadID      jmap.ID          `json:"threadId,omitempty"`
	MailboxIDs    map[jmap.ID]bool `json:"mailboxIds,omitempty"`
	Keywords      map[string]bool  `json:"keywords,omitempty"`
	Size          uint64           `json:"size,omitempty"`
	ReceivedAt    *time.Time       `json:"receivedAt,omitempty"`
	Headers       []*email.Header  `json:"headers,omitempty"`
	MessageID     []string         `json:"messageId,omitempty"`
	InReplyTo     []string         `json:"inReplyTo,omitempty"`
	References    []string         `json:"references,omitempty"`
	Sender        []*mail.Address  `json:"sender,omitempty"`
	From          []*mail.Address  `json:"from,omitempty"`
	To            []*mail.Address  `json:"to,omitempty"`
	CC            []*mail.Address  `json:"cc,omitempty"`
	BCC           []*mail.Address  `json:"bcc,omitempty"`
	ReplyTo       []*mail.Address  `json:"replyTo,omitempty"`
	Subject       string           `json:"subject,omitempty"`
	SentAt        *time.Time       `json:"sentAt,omitempty"`
	BodyStructure *partView        `json:"bodyStructure,omitempty"`
	TextBody      []*partView      `json:"textBody,omitempty"`
	HTMLBody      []*partView      `json:"htmlBody,omitempty"`
	Attachments   []*partView      `json:"attachments,omitempty"`
	HasAttachment bool             `json:"hasAttachment"`
	Preview       string           `json:"preview,omitempty"`
}

func (e *envelope) buildView() *messageView {
	em := e.Email
	v := &messageView{
		ID:            em.ID,
		AccountID:     e.AccountID,
		BlobID:        em.BlobID,
		ThreadID:      em.ThreadID,
		MailboxIDs:    em.MailboxIDs,
		Keywords:      em.Keywords,
		Size:          em.Size,
		ReceivedAt:    em.ReceivedAt,
		Headers:       em.Headers,
		MessageID:     em.MessageID,
		InReplyTo:     em.InReplyTo,
		References:    em.References,
		Sender:        em.Sender,
		From:          em.From,
		To:            em.To,
		CC:            em.CC,
		BCC:           em.BCC,
		ReplyTo:       em.ReplyTo,
		Subject:       em.Subject,
		SentAt:        em.SentAt,
		HasAttachment: em.HasAttachment,
		Preview:       em.Preview,
		BodyStructure: e.partViewOf(em.BodyStructure),
	}
	for _, p := range em.TextBody {
		v.TextBody = append(v.TextBody, e.partViewOf(p))
	}
	for _, p := range em.HTMLBody {
		v.HTMLBody = append(v.HTMLBody, e.partViewOf(p))
	}
	for _, p := range em.Attachments {
		v.Attachments = append(v.Attachments, e.partViewOf(p))
	}
	return v
}

func (e *envelope) partViewOf(p *email.BodyPart) *partView {
	if p == nil {
		return nil
	}
	pv := &partView{
		PartID:      p.PartID,
		BlobID:      p.BlobID,
		Size:        p.Size,
		Headers:     p.Headers,
		Name:        p.Name,
		Type:        p.Type,
		Charset:     p.Charset,
		Disposition: p.Disposition,
		CID:         p.CID,
		Language:    p.Language,
		Location:    p.Location,
	}
	for _, sub := range p.SubParts {
		pv.SubParts = append(pv.SubParts, e.partViewOf(sub))
	}
	if p.BlobID != "" {
		if res, ok := e.Parts[p.BlobID]; ok {
			if res.ObjectKey != "" {
				k := res.ObjectKey
				pv.ObjectKey = &k
			}
			pv.Skipped = res.Skipped
			pv.Error = res.Error
		}
	}
	return pv
}

func (e *envelope) NATSHeaders() nats.Header {
	h := nats.Header{}
	em := e.Email
	h.Set(natsHeaderSchemaVersion, wireSchemaVersion)
	h.Set("Jmap-Account-Id", string(e.AccountID))
	h.Set("Jmap-Email-Id", string(em.ID))
	if em.ThreadID != "" {
		h.Set("Jmap-Thread-Id", string(em.ThreadID))
	}
	if s := joinAddrs(em.From); s != "" {
		h.Set("Jmap-From", s)
	}
	if s := joinAddrs(em.To); s != "" {
		h.Set("Jmap-To", s)
	}
	if s := joinAddrs(em.CC); s != "" {
		h.Set("Jmap-Cc", s)
	}
	if em.Subject != "" {
		h.Set("Jmap-Subject", em.Subject)
	}
	if em.ReceivedAt != nil {
		h.Set("Jmap-Received-At", em.ReceivedAt.UTC().Format(time.RFC3339))
	}
	if em.SentAt != nil {
		h.Set("Jmap-Sent-At", em.SentAt.UTC().Format(time.RFC3339))
	}
	if len(em.MessageID) > 0 {
		h.Set("Jmap-Message-Id", strings.Join(em.MessageID, ","))
	}
	if len(em.InReplyTo) > 0 {
		h.Set("Jmap-In-Reply-To", strings.Join(em.InReplyTo, ","))
	}
	if len(em.References) > 0 {
		h.Set("Jmap-References", strings.Join(em.References, ","))
	}
	if len(em.MailboxIDs) > 0 {
		ids := make([]string, 0, len(em.MailboxIDs))
		for id := range em.MailboxIDs {
			ids = append(ids, string(id))
		}
		sort.Strings(ids)
		h.Set("Jmap-Mailbox-Ids", strings.Join(ids, ","))
	}
	if len(em.Keywords) > 0 {
		ks := make([]string, 0, len(em.Keywords))
		for k, v := range em.Keywords {
			if v {
				ks = append(ks, k)
			}
		}
		sort.Strings(ks)
		h.Set("Jmap-Keywords", strings.Join(ks, ","))
	}
	h.Set("Jmap-Has-Attachment", strconv.FormatBool(em.HasAttachment))
	h.Set("Jmap-Size", strconv.FormatUint(em.Size, 10))
	return h
}

func joinAddrs(addrs []*mail.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a == nil {
			continue
		}
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

type Bridge struct {
	cfg Config
	log *slog.Logger
	jc  *JMAPClient
	nr  *NATSResources
}

func NewBridge(cfg Config, log *slog.Logger, jc *JMAPClient, nr *NATSResources) *Bridge {
	return &Bridge{cfg: cfg, log: log, jc: jc, nr: nr}
}

func (b *Bridge) Run(ctx context.Context) error {
	accountID := string(b.jc.AccountID())
	cursor, err := b.nr.LoadCursor(ctx, accountID)
	if err != nil {
		return err
	}
	if cursor == "" {
		b.log.Info("no persisted cursor found; running bounded bootstrap recovery", "account_id", accountID)
		cursor, err = b.bootRecover(ctx)
		if err != nil {
			return fmt.Errorf("boot recovery: %w", err)
		}
		if err := b.nr.SaveCursor(ctx, accountID, cursor); err != nil {
			return fmt.Errorf("save boot cursor: %w", err)
		}
	} else {
		b.log.Info("loaded persisted cursor", "account_id", accountID, "state", cursor)
	}
	b.log.Info("entering push loop", "initial_state", cursor)

	trigger := make(chan struct{}, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		current := cursor
		for {
			select {
			case <-ctx.Done():
				return
			case <-trigger:
			}
			next, err := b.syncChanges(ctx, current)
			if err != nil {
				if errors.Is(err, errCannotCalculate) {
					b.log.Warn("server cannot calculate changes from persisted cursor; running bounded fallback recovery",
						"state", current, "backfill_limit", b.cfg.BackfillLimit)
					next, err = b.recoverExpiredCursor(ctx)
				}
				if err != nil {
					b.log.Error("sync failed", "err", err)
					continue
				}
			}
			if next != "" {
				current = next
			}
		}
	}()

	backoff := 200 * time.Millisecond
	const maxBackoff = 30 * time.Second
	for ctx.Err() == nil {
		err := b.listenOnce(ctx, trigger)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			b.log.Warn("eventsource disconnected", "err", err, "retry_in", backoff)
		} else {
			b.log.Info("eventsource closed; reconnecting", "retry_in", backoff)
		}
		if !sleep(ctx, backoff) {
			break
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	<-workerDone
	return ctx.Err()
}

var errCannotCalculate = errors.New("cannotCalculateChanges")

func (b *Bridge) recoverExpiredCursor(ctx context.Context) (string, error) {
	state, err := b.bootRecover(ctx)
	if err != nil {
		return "", err
	}
	if err := b.nr.SaveCursor(ctx, string(b.jc.AccountID()), state); err != nil {
		return "", fmt.Errorf("save recovered cursor: %w", err)
	}
	return state, nil
}

func (b *Bridge) bootRecover(ctx context.Context) (string, error) {
	highWater, err := b.nr.LastPublishedEmailID(ctx, string(b.jc.AccountID()))
	if err != nil {
		b.log.Warn("boot recovery: high-water lookup failed; processing full window", "err", err)
	}
	ids, err := b.jc.QueryRecent(b.cfg.BackfillLimit)
	if err != nil {
		return "", err
	}
	state, err := b.jc.FetchState()
	if err != nil {
		return "", err
	}
	b.log.Info("boot recovery", "ids_to_check", len(ids), "state", state, "high_water", highWater)
	reached := false
	for i, id := range ids {
		if ctx.Err() != nil {
			return state, ctx.Err()
		}
		if highWater != "" && string(id) == highWater {
			b.log.Info("boot recovery: reached high-water mark", "processed", i, "skipped", len(ids)-i)
			reached = true
			break
		}
		if err := b.processOne(ctx, id); err != nil {
			b.log.Error("boot recovery: process failed", "id", id, "err", err)
		}
	}
	if highWater != "" && !reached {
		b.log.Warn("boot recovery: high-water mark not found in backfill window; consider raising backfill_limit",
			"high_water", highWater, "backfill_limit", b.cfg.BackfillLimit)
	}
	return state, nil
}

func (b *Bridge) syncChanges(ctx context.Context, sinceState string) (string, error) {
	created, newState, err := b.jc.Changes(sinceState)
	if err != nil {
		if me := (*jmap.MethodError)(nil); errors.As(err, &me) && me.Type == "cannotCalculateChanges" {
			return "", errCannotCalculate
		}
		return "", err
	}
	if len(created) == 0 {
		if err := b.nr.SaveCursor(ctx, string(b.jc.AccountID()), newState); err != nil {
			return sinceState, err
		}
		return newState, nil
	}
	b.log.Info("sync: new emails", "count", len(created), "since", sinceState, "now", newState)
	for _, id := range created {
		if ctx.Err() != nil {
			return newState, ctx.Err()
		}
		if err := b.processOne(ctx, id); err != nil {
			b.log.Error("process failed", "id", id, "err", err)
		}
	}
	if err := b.nr.SaveCursor(ctx, string(b.jc.AccountID()), newState); err != nil {
		return sinceState, err
	}
	return newState, nil
}

func (b *Bridge) listenOnce(ctx context.Context, trigger chan<- struct{}) error {
	es := &push.EventSource{
		Client:  b.jc.Client(),
		Handler: func(sc *jmap.StateChange) { b.onStateChange(sc, trigger) },
		Events:  []jmap.EventType{mail.EmailEvent},
		Ping:    60,
	}
	select {
	case trigger <- struct{}{}:
	default:
	}
	iterCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-iterCtx.Done()
		es.Close()
	}()
	b.log.Info("eventsource: connecting")
	err := es.Listen()
	if err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (b *Bridge) onStateChange(sc *jmap.StateChange, trigger chan<- struct{}) {
	if sc == nil {
		return
	}
	ts, ok := sc.Changed[b.jc.AccountID()]
	if !ok {
		return
	}
	state, ok := ts["Email"]
	if !ok {
		return
	}
	b.log.Debug("statechange received", "email_state", state)
	select {
	case trigger <- struct{}{}:
	default:
		b.log.Debug("statechange coalesced; sync already pending")
	}
}

func (b *Bridge) processOne(ctx context.Context, id jmap.ID) error {
	if !validJMAPID(string(id)) {
		return fmt.Errorf("invalid JMAP email id %q (RFC 8620 §1.2: A-Za-z0-9_-)", id)
	}
	em, err := b.jc.FetchEmail(id)
	if err != nil {
		return err
	}
	env := &envelope{
		AccountID: b.jc.AccountID(),
		Email:     em,
		Parts:     make(map[jmap.ID]*partResult),
	}
	b.storeParts(ctx, env)
	body, err := json.Marshal(env.buildView())
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	dup, err := b.nr.Publish(ctx, env, body)
	if err != nil {
		return err
	}
	if dup {
		b.log.Debug("published (dedup)", "id", id, "subject", em.Subject)
	} else {
		b.log.Info("published", "id", id, "subject", em.Subject)
	}
	return nil
}

func (b *Bridge) storeParts(ctx context.Context, env *envelope) {
	maxBytes := uint64(b.cfg.Parts.MaxPerPart.Int64())
	textBodies := textBodyPartIDs(env.Email)
	visit := func(p *email.BodyPart) {
		if p == nil || p.BlobID == "" {
			return
		}
		if _, done := env.Parts[p.BlobID]; done {
			return
		}
		res := &partResult{}
		env.Parts[p.BlobID] = res
		if !validJMAPID(string(p.BlobID)) {
			res.Error = "invalid blob id"
			return
		}
		if p.Size > maxBytes {
			res.Skipped = true
			b.log.Debug("part skipped (too large)",
				"email_id", env.Email.ID, "blob_id", p.BlobID,
				"size", p.Size, "max", maxBytes, "type", p.Type)
			return
		}
		var src io.Reader
		var closer io.Closer
		if textBodies[p.PartID] {
			text, err := textBodyValue(env.Email.BodyValues, p.PartID)
			if err != nil {
				res.Error = err.Error()
				return
			}
			src = strings.NewReader(text)
		} else {
			rc, err := b.jc.Client().DownloadWithContext(ctx, env.AccountID, p.BlobID)
			if err != nil {
				res.Error = "download: " + err.Error()
				return
			}
			src = rc
			closer = rc
		}
		key, err := b.nr.PutPart(ctx, string(env.AccountID), string(env.Email.ID), string(p.BlobID), src)
		if closer != nil {
			_ = closer.Close()
		}
		if err != nil {
			res.Error = "put: " + err.Error()
			return
		}
		res.ObjectKey = key
		b.log.Debug("part stored",
			"email_id", env.Email.ID, "blob_id", p.BlobID,
			"size", p.Size, "type", p.Type, "key", key)
	}
	walkBodyParts(env.Email.BodyStructure, visit)
	for _, p := range env.Email.TextBody {
		visit(p)
	}
	for _, p := range env.Email.HTMLBody {
		visit(p)
	}
	for _, p := range env.Email.Attachments {
		visit(p)
	}
}

func textBodyPartIDs(em *email.Email) map[string]bool {
	ids := map[string]bool{}
	add := func(parts []*email.BodyPart) {
		for _, p := range parts {
			if p == nil || p.PartID == "" || !isTextPart(p) {
				continue
			}
			ids[p.PartID] = true
		}
	}
	add(em.TextBody)
	add(em.HTMLBody)
	return ids
}

func textBodyValue(values map[string]*email.BodyValue, partID string) (string, error) {
	if partID == "" {
		return "", fmt.Errorf("body value missing: empty partId")
	}
	bv, ok := values[partID]
	if !ok || bv == nil {
		return "", fmt.Errorf("body value missing: partId %s", partID)
	}
	if bv.IsTruncated {
		return "", fmt.Errorf("body value truncated: partId %s", partID)
	}
	if bv.IsEncodingProblem {
		return "", fmt.Errorf("body value encoding problem: partId %s", partID)
	}
	return normalizeLineEndings(bv.Value), nil
}

func isTextPart(p *email.BodyPart) bool {
	return strings.HasPrefix(strings.ToLower(p.Type), "text/")
}

func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func walkBodyParts(p *email.BodyPart, f func(*email.BodyPart)) {
	if p == nil {
		return
	}
	f(p)
	for _, sub := range p.SubParts {
		walkBodyParts(sub, f)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
