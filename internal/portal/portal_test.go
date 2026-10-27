package portal

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestByteDisplaysAndEditValues(t *testing.T) {
	for _, tc := range []struct {
		value int64
		want  string
	}{{0, "0 bytes"}, {512, "512 bytes"}, {1024, "1 KiB"}, {12345, "12.06 KiB"}, {65536, "64 KiB"}, {1 << 20, "1 MiB"}, {1 << 30, "1 GiB"}, {1536 << 20, "1.5 GiB"}, {1 << 40, "1 TiB"}, {math.MaxInt64, "8 EiB"}} {
		if got := FormatBytes(tc.value); got != tc.want {
			t.Fatalf("FormatBytes(%d) = %s, want %s", tc.value, got, tc.want)
		}
	}
	// An untouched administrator override must preserve every byte, including
	// arbitrary limits and values too large to round-trip through float64.
	for _, value := range []int64{0, 12345, 1 << 30, 1536 << 20, math.MaxInt64} {
		input := byteInput(strconv.FormatInt(value, 10))
		quantity, err := strconv.ParseInt(input.Value, 10, 64)
		factor := map[string]int64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[input.Unit]
		if err != nil || factor == 0 || quantity*factor != value {
			t.Fatalf("quota edit changed %d to %+v", value, input)
		}
	}
}

func TestErrorPagesKeepStatusAndOfferTaskSizedRecovery(t *testing.T) {
	cases := []struct {
		status int
		want   string
		avoid  string
	}{{http.StatusUnauthorized, "/auth/login", "href=\"/admin\""}, {http.StatusForbidden, "Check your relay account", "/auth/login"}, {http.StatusNotFound, "could not be found", "/auth/login"}, {http.StatusTooManyRequests, "Retry-After: 7", "/auth/login"}, {http.StatusServiceUnavailable, "may not have been saved", "/auth/login"}}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			w := httptest.NewRecorder()
			if tc.status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "7")
			}
			Error(w, tc.status)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), tc.avoid) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRecentAuthenticationFailureRequiresFreshSignInWithoutReplayingDeletion(t *testing.T) {
	w := httptest.NewRecorder()
	RecentAuthenticationRequired(w)
	body := w.Body.String()
	if w.Code != http.StatusForbidden || !strings.Contains(body, "Sign in again to continue") || !strings.Contains(body, "return to Access &amp; security and submit deletion again") || !strings.Contains(body, `href="/auth/login"`) || strings.Contains(body, `href="/auth/delete"`) {
		t.Fatalf("recent-auth recovery did not require a deliberate resubmission: %d %s", w.Code, body)
	}
}

func TestConflictPageUsesSafeAffectedObjectReload(t *testing.T) {
	w := httptest.NewRecorder()
	Conflict(w, "/admin?selected=123&view=detail", "Reload this account")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `href="/admin?selected=123&amp;view=detail"`) || !strings.Contains(w.Body.String(), "Reload this account") {
		t.Fatalf("conflict recovery = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	Conflict(w, "//attacker.invalid", "Continue")
	if !strings.Contains(w.Body.String(), `href="/admin"`) || strings.Contains(w.Body.String(), "attacker.invalid") {
		t.Fatalf("unsafe conflict destination rendered: %s", w.Body.String())
	}
}

func TestUnavailablePlanActionOffersSafeLibraryRead(t *testing.T) {
	w := httptest.NewRecorder()
	Unavailable(w, "/admin/plans", "Check quota plans")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `href="/admin/plans"`) || !strings.Contains(w.Body.String(), "Check quota plans") || strings.Contains(w.Body.String(), "Retry this plan") {
		t.Fatalf("plan uncertainty recovery = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	Unavailable(w, "//attacker.invalid", "Continue")
	if !strings.Contains(w.Body.String(), `href="/"`) || strings.Contains(w.Body.String(), "attacker.invalid") {
		t.Fatalf("unsafe unavailable destination rendered: %s", w.Body.String())
	}
}

func TestInvalidPlanActionOffersLibraryCorrectionWithoutChangingStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		w := httptest.NewRecorder()
		PlanRequestError(w, status)
		if w.Code != status || !strings.Contains(w.Body.String(), `href="/admin/plans"`) || !strings.Contains(w.Body.String(), "Return to quota plans") {
			t.Fatalf("plan status %d lost safe correction: %d %s", status, w.Code, w.Body.String())
		}
	}
}

func TestSetupAndUncertainMessagesStayCompactAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"setup", http.StatusOK}, {"uncertain", http.StatusServiceUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			Render(w, tc.status, tc.name, nil)
			body := w.Body.String()
			if w.Code != tc.status || !strings.Contains(body, "message-sheet") || strings.Contains(body, "<form") {
				t.Fatalf("%s message is not a read-only compact page: %d %s", tc.name, w.Code, body)
			}
			if tc.name == "uncertain" && (!strings.Contains(body, "Access has been closed") || !strings.Contains(body, `href="/"`)) {
				t.Fatalf("uncertain result did not preserve fail-closed recovery: %s", body)
			}
		})
	}
}
