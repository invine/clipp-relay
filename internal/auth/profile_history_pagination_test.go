package auth_test

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/quota"
)

// historyLimitReadViews lets the authenticated portal exercise snapshots with
// fewer retained windows while preserving the real account-scoped read.
type historyLimitReadViews struct {
	persistence.ReadViews
	limit int
}

func (r *historyLimitReadViews) OwnerProfile(ctx context.Context, id string, digest []byte, version uint64) (p persistence.OwnerProfile, err error) {
	p, err = r.ReadViews.OwnerProfile(ctx, id, digest, version)
	if err != nil {
		return p, err
	}
	var entries []json.RawMessage
	if err = json.Unmarshal(p.HistoryJSON, &entries); err != nil {
		return p, err
	}
	if len(entries) > r.limit {
		entries = entries[:r.limit]
	}
	p.HistoryJSON, err = json.Marshal(entries)
	return p, err
}

var historyDate = regexp.MustCompile(`<time datetime="([0-9-]+)">`)
var showMoreLink = regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>Show more</a>`)

func TestOwnerHistoryProgressiveDisclosure(t *testing.T) {
	c, m, db := fixture(t)
	provider := newProvider(t)
	deps := authDependencies(db)
	reads := &historyLimitReadViews{ReadViews: deps.ReadViews, limit: 53}
	deps.ReadViews = reads
	s := auth.New(deps, c, m, provider.endpoints())
	accounting := quota.New(nil)
	t.Cleanup(accounting.Close)
	if err := s.SetDeletionAccountingForTest(accounting); err != nil {
		t.Fatal(err)
	}
	subject := "history-pages-" + time.Now().UTC().Format("20060102150405.000000000")
	cookie := loginAs(t, s, provider, subject, subject+"@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',recovery_review_hold=false,plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_delta_bytes) SELECT id,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,0,0,0 FROM public.accounts WHERE subject=$1 ON CONFLICT (account_id,week_start) DO NOTHING`, subject); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		limit int
		pages []int
	}{
		{3, []int{3}}, {4, []int{4}}, {5, []int{4, 5}}, {53, []int{4, 14, 24, 34, 44, 53}},
	} {
		reads.limit = tc.limit
		path := "/"
		var previous []string
		for page, want := range tc.pages {
			response := portalRequest(s, "GET", path, cookie, nil, "")
			if response.Code != 200 {
				t.Fatalf("limit %d page %d: HTTP %d", tc.limit, page, response.Code)
			}
			body := response.Body.String()
			start := strings.Index(body, `class="history-table"`)
			if start < 0 {
				t.Fatalf("limit %d page %d: no table", tc.limit, page)
			}
			end := strings.Index(body[start:], "</table>")
			if end < 0 {
				t.Fatal("unterminated history table")
			}
			matches := historyDate.FindAllStringSubmatch(body[start:start+end], -1)
			if len(matches) != want {
				t.Fatalf("limit %d page %d: got %d rows, want %d", tc.limit, page, len(matches), want)
			}
			for i, match := range matches {
				if i < len(previous) && match[1] != previous[i] {
					t.Fatalf("limit %d page %d: previous row %d changed", tc.limit, page, i)
				}
				if i > 0 && matches[i-1][1] <= match[1] {
					t.Fatalf("limit %d page %d: dates are not newest first", tc.limit, page)
				}
			}
			previous = make([]string, len(matches))
			for i, match := range matches {
				previous[i] = match[1]
			}
			link := showMoreLink.FindStringSubmatch(body)
			if page == len(tc.pages)-1 {
				if link != nil {
					t.Fatalf("limit %d: Show more remains after final row", tc.limit)
				}
			} else {
				if link == nil {
					t.Fatalf("limit %d page %d: Show more missing", tc.limit, page)
				}
				// Browsers do not send URL fragments to the server.
				path, _, _ = strings.Cut(html.UnescapeString(link[1]), "#")
			}
		}
	}
}
