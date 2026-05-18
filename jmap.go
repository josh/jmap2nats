package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

const maxBodyValueBytes = 10 * 1024 * 1024

type JMAPClient struct {
	cfg       Config
	log       *slog.Logger
	client    *jmap.Client
	accountID jmap.ID
}

func NewJMAPClient(cfg Config, log *slog.Logger) (*JMAPClient, error) {
	tokenBytes, err := os.ReadFile(cfg.JMAP.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("jmap read token_file %s: %w", cfg.JMAP.TokenFile, err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("jmap token_file %s is empty", cfg.JMAP.TokenFile)
	}

	client := &jmap.Client{SessionEndpoint: cfg.JMAP.SessionURL}
	client.WithAccessToken(token)
	if err := client.Authenticate(); err != nil {
		return nil, fmt.Errorf("jmap authenticate: %w", err)
	}

	var accountID jmap.ID
	if cfg.JMAP.AccountID != "" {
		accountID = jmap.ID(cfg.JMAP.AccountID)
		if _, ok := client.Session.Accounts[accountID]; !ok {
			return nil, fmt.Errorf("jmap account %q not in session.accounts", cfg.JMAP.AccountID)
		}
	} else {
		accountID = client.Session.PrimaryAccounts[mail.URI]
		if accountID == "" {
			return nil, fmt.Errorf("jmap session has no primary mail account; set jmap.account_id")
		}
	}
	log.Info("jmap authenticated",
		"session_url", cfg.JMAP.SessionURL,
		"username", client.Session.Username,
		"account_id", accountID,
	)
	return &JMAPClient{cfg: cfg, log: log, client: client, accountID: accountID}, nil
}

func (j *JMAPClient) AccountID() jmap.ID   { return j.accountID }
func (j *JMAPClient) Client() *jmap.Client { return j.client }

func (j *JMAPClient) QueryRecent(limit uint64) ([]jmap.ID, error) {
	req := &jmap.Request{}
	req.Invoke(&email.Query{
		Account: j.accountID,
		Sort: []*email.SortComparator{
			{Property: "receivedAt", IsAscending: false},
		},
		Limit: limit,
	})
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jmap Email/query: %w", err)
	}
	for _, inv := range resp.Responses {
		if q, ok := inv.Args.(*email.QueryResponse); ok {
			return q.IDs, nil
		}
	}
	return nil, fmt.Errorf("jmap Email/query: no QueryResponse in reply")
}

func (j *JMAPClient) Changes(sinceState string) (created []jmap.ID, newState string, err error) {
	state := sinceState
	for {
		req := &jmap.Request{}
		req.Invoke(&email.Changes{
			Account:    j.accountID,
			SinceState: state,
		})
		resp, err := j.client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("jmap Email/changes: %w", err)
		}
		var cr *email.ChangesResponse
		for _, inv := range resp.Responses {
			switch r := inv.Args.(type) {
			case *email.ChangesResponse:
				cr = r
			case *jmap.MethodError:
				return nil, "", r
			}
		}
		if cr == nil {
			return nil, "", fmt.Errorf("jmap Email/changes: no ChangesResponse in reply")
		}
		created = append(created, cr.Created...)
		state = cr.NewState
		if !cr.HasMoreChanges {
			return created, state, nil
		}
	}
}

func (j *JMAPClient) FetchState() (string, error) {
	req := &jmap.Request{}
	req.Invoke(&email.Get{
		Account:    j.accountID,
		IDs:        []jmap.ID{"jmap2nats-state-probe"},
		Properties: []string{"id"},
	})
	resp, err := j.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("jmap Email/get (state probe): %w", err)
	}
	for _, inv := range resp.Responses {
		if gr, ok := inv.Args.(*email.GetResponse); ok {
			return gr.State, nil
		}
	}
	return "", fmt.Errorf("jmap Email/get (state probe): no GetResponse in reply")
}

func (j *JMAPClient) FetchEmail(id jmap.ID) (*email.Email, error) {
	req := &jmap.Request{}
	req.Invoke(&email.Get{
		Account:             j.accountID,
		IDs:                 []jmap.ID{id},
		FetchTextBodyValues: true,
		FetchHTMLBodyValues: true,
		MaxBodyValueBytes:   maxBodyValueBytes,
	})
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jmap Email/get %s: %w", id, err)
	}
	for _, inv := range resp.Responses {
		gr, ok := inv.Args.(*email.GetResponse)
		if !ok {
			continue
		}
		if len(gr.List) > 0 {
			return gr.List[0], nil
		}
		if len(gr.NotFound) > 0 {
			return nil, fmt.Errorf("jmap Email/get %s: not found", id)
		}
	}
	return nil, fmt.Errorf("jmap Email/get %s: empty response", id)
}
