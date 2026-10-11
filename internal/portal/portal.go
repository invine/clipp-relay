// Package portal owns the embedded presentation shared by the relay's browser pages.
package portal

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html assets/*
var files embed.FS

var pages = template.Must(template.New("portal").Funcs(template.FuncMap{
	"bytes":     FormatBytes,
	"byteInput": byteInput,
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
	message := "Please return to your account and try again."
	switch status {
	case http.StatusUnauthorized:
		message = "Your sign-in may have expired. Sign in with Google again, then retry the action."
	case http.StatusForbidden:
		message = "This action is unavailable for your current sign-in. Return to your account to check your access."
	case http.StatusConflict:
		message = "The saved account or plan has changed. Reload it before trying again."
	case http.StatusTooManyRequests:
		message = "Please wait a moment before trying again."
	case http.StatusServiceUnavailable:
		message = "The relay portal is temporarily unavailable. Please try again shortly."
	case http.StatusNotFound:
		message = "This page could not be found. Return to your relay account."
	}
	Render(w, status, "error", struct {
		Status  int
		Message string
	}{status, message})
}
