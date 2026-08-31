//go:build integration

// Integration test against a live Mailpit. Start it with `make up`, then:
//
//	cd shared && go test -tags integration ./mail/mailpit/
//
// It is behind a build tag so the default `make test` stays hermetic.
package mailpit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/mail/mailpit"
)

// mailpitAPI is the base URL of Mailpit's HTTP API, used only to read back what
// was delivered. The connector itself speaks plain SMTP.
func mailpitAPI() string {
	return "http://" + config.String("MAILPIT_HOST", "localhost") + ":" +
		config.String("MAILPIT_WEB_PORT", "8025")
}

type messagesResponse struct {
	Total    int `json:"total"`
	Messages []struct {
		ID      string `json:"ID"`
		Subject string `json:"Subject"`
		To      []struct {
			Address string `json:"Address"`
		} `json:"To"`
	} `json:"messages"`
}

func TestSendReachesMailpit(t *testing.T) {
	cfg := mailpit.LoadConfig()
	mailer := mailpit.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mailer.Ping(ctx); err != nil {
		t.Skipf("Mailpit is not reachable at %s (run `make up`): %v", cfg.Addr(), err)
	}

	// A unique subject keeps this test independent of whatever else is in the
	// mailbox.
	subject := fmt.Sprintf("boilerplate integration %d", time.Now().UnixNano())

	err := mailer.Send(ctx, mailpit.Message{
		To:       []string{"someone@example.test"},
		CC:       []string{"cc@example.test"},
		BCC:      []string{"bcc@example.test"},
		Subject:  subject,
		TextBody: "plain text body\nsecond line",
		HTMLBody: "<h1>html body</h1>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	found := waitForMessage(t, subject)
	if found == "" {
		t.Fatalf("message %q never arrived in Mailpit", subject)
	}
	t.Logf("delivered, Mailpit message ID %s", found)
}

// waitForMessage polls Mailpit until the subject shows up, since SMTP delivery
// and indexing are not synchronous with the SMTP QUIT.
func waitForMessage(t *testing.T, subject string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		res, err := http.Get(mailpitAPI() + "/api/v1/messages?limit=50") //nolint:noctx // short-lived test poll
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		var parsed messagesResponse
		decodeErr := json.NewDecoder(res.Body).Decode(&parsed)
		res.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode Mailpit response: %v", decodeErr)
		}

		for _, m := range parsed.Messages {
			if m.Subject == subject {
				return m.ID
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return ""
}

func TestSendRejectsMessageWithNoRecipient(t *testing.T) {
	mailer := mailpit.New(mailpit.LoadConfig())

	if err := mailer.Send(context.Background(), mailpit.Message{Subject: "nobody"}); err == nil {
		t.Fatal("expected an error for a message with no recipient")
	}
}
