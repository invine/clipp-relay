// Package portal owns the embedded presentation shared by the relay's browser pages.
package portal

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

//go:embed templates/*.html assets/*
var files embed.FS

var pages = template.Must(template.New("portal").Funcs(template.FuncMap{
	"bytes":        FormatBytes,
	"byteInput":    byteInput,
	"histogram":    quotaHistogram,
	"emailPattern": func(email string) string { return regexp.QuoteMeta(email) },
	"clientName": func(client string) string {
		switch client {
		case "electron":
			return "Clipp desktop"
		case "android":
			return "Clipp for Android"
		case "extension":
			return "Clipp Chrome extension"
		}
		return "Clipp"
	},
}).ParseFS(files, "templates/*.html"))

// Render buffers before writing so a template failure cannot produce a partial page.
func Render(w http.ResponseWriter, status int, name string, data any) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

// RegisterAssets serves only the two public presentation assets, without exposing templates.
func RegisterAssets(mux *http.ServeMux) {
	for name, contentType := range map[string]string{"portal.css": "text/css; charset=utf-8", "portal.js": "text/javascript; charset=utf-8"} {
		body, err := files.ReadFile("assets/" + name)
		if err != nil {
			panic(err)
		}
		mux.HandleFunc("GET /assets/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			// Revalidate on every load so an in-place relay upgrade also updates styling.
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(body)
		})
	}
}

// FormatBytes uses binary units, matching the relay's 1 GiB baseline allowance.
// Exact byte counts remain available in titles on quota displays.
func FormatBytes(value int64) string {
	if value < 1024 {
		return strconv.FormatInt(value, 10) + " bytes"
	}
	amount := float64(value)
	units := []string{"bytes", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", amount), "0"), ".") + " " + units[unit]
}

type quotaInput struct{ Value, Unit string }

// Editing uses the largest exact whole unit so saving an unchanged override
// never rounds the original allowance. Arbitrary byte allowances remain exact.
func byteInput(raw string) quotaInput {
	if raw == "" {
		return quotaInput{"", "GiB"}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return quotaInput{raw, "B"}
	}
	if value == 0 {
		return quotaInput{"0", "GiB"}
	}
	for _, unit := range []struct {
		name   string
		factor int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if value >= unit.factor && value%unit.factor == 0 {
			return quotaInput{strconv.FormatInt(value/unit.factor, 10), unit.name}
		}
	}
	return quotaInput{raw, "B"}
}

func Error(w http.ResponseWriter, status int) {
	message := "This request could not be completed. Check your relay account for its current state."
	title, action, href := "Request could not be completed", "Check your relay account", "/"
	switch status {
	case http.StatusUnauthorized:
		title, message, action, href = "Sign-in expired", "Your portal session has ended. Sign in again, then return to the task you were doing.", "Sign in with Google", "/auth/login"
	case http.StatusForbidden:
		title, message = "This action is not available", "Your current sign-in cannot perform this action. Check your account or contact a relay administrator if you think this is a mistake."
	case http.StatusConflict:
		title, message = "This information has changed", "Reload the affected account or plan to see its latest state before choosing what to do next."
	case http.StatusTooManyRequests:
		title, message, action = "Please wait before continuing", "Requests are temporarily limited. Return to your account and check its current state before trying again.", "Check your relay account"
	case http.StatusServiceUnavailable:
		title, message = "The relay portal is temporarily unavailable", "Your request may not have been saved. Check the relevant account or operation before deciding whether to submit it again."
	case http.StatusNotFound:
		title, message = "This page could not be found", "The page may have moved or may no longer be available. Return to your relay account."
	}
	Render(w, status, "error", struct {
		Status                                   int
		Title, Message, Action, Href, RetryAfter string
	}{status, title, message, action, href, w.Header().Get("Retry-After")})
}

// RecentAuthenticationRequired explains the protected deletion gate without
// submitting the operation again after the owner completes Google sign-in.
func RecentAuthenticationRequired(w http.ResponseWriter) {
	Render(w, http.StatusForbidden, "error", struct {
		Status                                   int
		Title, Message, Action, Href, RetryAfter string
	}{http.StatusForbidden, "Sign in again to continue", "Account deletion requires a recent Google sign-in. Sign in, then return to Access & security and submit deletion again.", "Sign in with Google", "/auth/login", ""})
}

// Conflict renders a conflict with a server-constructed safe reload destination.
func Conflict(w http.ResponseWriter, href, label string) {
	if !strings.HasPrefix(href, "/admin") || strings.HasPrefix(href, "//") {
		href, label = "/admin", "Reload relay administration"
	}
	Render(w, http.StatusConflict, "error", struct {
		Status                                   int
		Title, Message, Action, Href, RetryAfter string
	}{http.StatusConflict, "This information has changed", "Reload the affected account or plan to see its latest state before choosing what to do next.", label, href, w.Header().Get("Retry-After")})
}

// Unavailable keeps an uncertain action on a safe, task-specific read path.
func Unavailable(w http.ResponseWriter, href, label string) {
	if !strings.HasPrefix(href, "/admin") || strings.HasPrefix(href, "//") {
		href, label = "/", "Check your relay account"
	}
	Render(w, http.StatusServiceUnavailable, "error", struct {
		Status                                   int
		Title, Message, Action, Href, RetryAfter string
	}{http.StatusServiceUnavailable, "The relay portal is temporarily unavailable", "Your request may not have been saved. Reload the plan library to check its current state before deciding whether to submit another action.", label, href, w.Header().Get("Retry-After")})
}

// PlanRequestError preserves the request status while returning administrators
// to the correct read-only place to correct a plan action.
func PlanRequestError(w http.ResponseWriter, status int) {
	title := "Check the quota plan request"
	message := "The plan request could not be accepted. Return to the quota plan library to correct its fields or review the current plan state."
	if status == http.StatusForbidden {
		title, message = "This plan action is not available", "Your current administrator sign-in cannot perform this action. Return to the quota plan library or contact a relay administrator."
	} else if status == http.StatusNotFound {
		title, message = "This quota plan is no longer available", "Reload the quota plan library to see the current plans before choosing another action."
	}
	Render(w, status, "error", struct {
		Status                                   int
		Title, Message, Action, Href, RetryAfter string
	}{status, title, message, "Return to quota plans", "/admin/plans", w.Header().Get("Retry-After")})
}
