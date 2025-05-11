// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// RodBrowser represents a go-rod browser instance with helper methods for testing.
type RodBrowser struct {
	t        *testing.T
	browser  *rod.Browser
	launcher *launcher.Launcher
	headless bool
}

// RodPage represents a go-rod page with helper methods for testing.
type RodPage struct {
	t    *testing.T
	page *rod.Page
}

// NewRodBrowser creates a new RodBrowser instance.
// By default, the browser is launched in headless mode.
func NewRodBrowser(t *testing.T, opts ...RodOption) *RodBrowser {
	t.Helper()

	rb := &RodBrowser{
		t:        t,
		headless: true, // Default to headless
	}

	// Apply options
	for _, opt := range opts {
		opt(rb)
	}

	// Create launcher
	rb.launcher = launcher.New().
		Headless(rb.headless).
		Delete("disable-extensions")

	if !rb.headless {
		rb.launcher.Delete("disable-gpu")
	}

	// Launch browser
	controlURL := rb.launcher.MustLaunch()
	rb.browser = rod.New().ControlURL(controlURL).MustConnect()

	// Setup cleanup
	t.Cleanup(func() {
		rb.Close()
	})

	return rb
}

// RodOption is a function that configures a RodBrowser.
type RodOption func(*RodBrowser)

// WithHeadless configures whether the browser should run in headless mode.
func WithHeadless(headless bool) RodOption {
	return func(rb *RodBrowser) {
		rb.headless = headless
	}
}

// Close closes the browser.
func (rb *RodBrowser) Close() {
	if rb.browser != nil {
		rb.browser.MustClose()
		rb.browser = nil
	}
}

// IsHeadless returns whether the browser is running in headless mode.
func (rb *RodBrowser) IsHeadless() bool {
	return rb.headless
}

// MustPage navigates to the given URL and returns a RodPage.
func (rb *RodBrowser) MustPage(url string) *RodPage {
	rb.t.Helper()
	rb.t.Logf("Navigating to %s", url)

	page := rb.browser.MustPage(url)

	// Setup cleanup
	rb.t.Cleanup(func() {
		page.MustClose()
	})

	return &RodPage{
		t:    rb.t,
		page: page,
	}
}

// WaitForLoad waits for the page to load.
func (rp *RodPage) WaitForLoad() *RodPage {
	rp.t.Helper()
	rp.page.MustWaitLoad()
	return rp
}

// WaitForNetworkIdle waits for the network to be idle.
func (rp *RodPage) WaitForNetworkIdle(timeout time.Duration) *RodPage {
	rp.t.Helper()
	// Wait a bit for network to settle
	time.Sleep(timeout)
	return rp
}

// MustElement finds an element by selector and returns it.
func (rp *RodPage) MustElement(selector string) *rod.Element {
	rp.t.Helper()
	return rp.page.MustElement(selector)
}

// MustElementR finds an element by selector and regex and returns it.
func (rp *RodPage) MustElementR(selector, regex string) *rod.Element {
	rp.t.Helper()
	return rp.page.MustElementR(selector, regex)
}

// MustHas checks if the page has an element matching the selector.
func (rp *RodPage) MustHas(selector string) bool {
	rp.t.Helper()
	has, _, err := rp.page.Has(selector)
	if err != nil {
		rp.t.Fatalf("Error checking if page has element %s: %v", selector, err)
	}
	return has
}

// MustContainText checks if the page contains the given text.
func (rp *RodPage) MustContainText(text string) bool {
	rp.t.Helper()
	content, err := rp.page.HTML()
	if err != nil {
		rp.t.Fatalf("Error getting page HTML: %v", err)
	}
	return strings.Contains(content, text)
}

// VerifyPageLoaded verifies that the page has loaded correctly.
// It checks for the presence of a specific element (default: "body").
func (rp *RodPage) VerifyPageLoaded(selectors ...string) *RodPage {
	rp.t.Helper()

	selector := "body"
	if len(selectors) > 0 && selectors[0] != "" {
		selector = selectors[0]
	}

	// Wait for the element to be present
	rp.page.MustElement(selector)
	rp.t.Logf("Page loaded successfully (verified element: %s)", selector)

	return rp
}

// GetConsoleLog returns the console log entries from the page.
func (rp *RodPage) GetConsoleLog() []string {
	rp.t.Helper()

	logs := []string{}

	// Execute JavaScript to get console logs
	// This assumes console logs are stored in window.consoleLog
	result, err := rp.page.Eval(`() => {
		if (!window.consoleLog) return [];
		return window.consoleLog;
	}`)

	if err != nil {
		rp.t.Logf("Error getting console logs: %v", err)
		return logs
	}

	// Try to unmarshal the result
	err = result.Value.Unmarshal(&logs)
	if err != nil {
		rp.t.Logf("Error unmarshaling console logs: %v", err)
	}

	return logs
}

// GetBrokerWSURL converts an HTTP test server URL to a WebSocket URL for the broker.
// It replaces "http://" with "ws://" and appends the WebSocket path.
func GetBrokerWSURL(server *httptest.Server, wsPath string) string {
	if wsPath == "" {
		wsPath = "/ws"
	}

	// If wsPath doesn't start with a slash, add one
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}

	// Replace http:// with ws://
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// Append the WebSocket path
	return wsURL + wsPath
}

// WaitForElement waits for an element to appear on the page.
func (rp *RodPage) WaitForElement(selector string, timeout time.Duration) *rod.Element {
	rp.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var element *rod.Element
	var err error

	for {
		select {
		case <-ctx.Done():
			rp.t.Fatalf("Timeout waiting for element %s: %v", selector, ctx.Err())
			return nil
		default:
			element, err = rp.page.Element(selector)
			if err == nil && element != nil {
				return element
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// Screenshot takes a screenshot of the page and saves it to the given path.
func (rp *RodPage) Screenshot(path string) *RodPage {
	rp.t.Helper()

	// Take screenshot
	data, err := rp.page.Screenshot(false, nil)
	if err != nil {
		rp.t.Logf("Error taking screenshot: %v", err)
		return rp
	}

	// Write to file
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		rp.t.Logf("Error saving screenshot to %s: %v", path, err)
	} else {
		rp.t.Logf("Screenshot saved to %s", path)
	}

	return rp
}

// Click clicks on an element matching the selector.
func (rp *RodPage) Click(selector string) *RodPage {
	rp.t.Helper()
	rp.page.MustElement(selector).MustClick()
	return rp
}

// Input types text into an element matching the selector.
func (rp *RodPage) Input(selector, text string) *RodPage {
	rp.t.Helper()
	rp.page.MustElement(selector).MustInput(text)
	return rp
}
