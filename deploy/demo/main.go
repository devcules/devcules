// Command demo-store is the sample app the PLAN.md §11 demo drives:
// one HTML checkout page whose Pay button is drawn on a canvas, so a
// regular DOM click cannot reach it and the agent must use the
// machine helper's grounding. The seeded bug is that the page labels
// the shipping total in the wrong currency until you pay (an officer
// of the demo wants the agent to spot and fix the label, not the
// charge); a button press after the fix shows "paid".
package main

import (
	"log"
	"net/http"
	"os"
)

// page has the Pay control on a canvas (drawn, not in the DOM's
// accessibility tree) and the bug the demo's agent is asked to fix:
// the "ships in $" hint would say CAD on Friday, JS-toggled, with
// `data-bug="currency"` marking the span the ask names. It is a real
// checkout in no sense — one page, no state, no money.
const page = `<!doctype html>
<meta charset="utf-8">
<title>Devcules demo store</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 540px; margin: 4rem auto; padding: 0 1rem; }
  h1 { font-size: 1.25rem; }
  #total { font-size: 2rem; font-weight: 600; margin: 1rem 0; }
  #pay { width: 220px; height: 72px; }
</style>

<h1>Demo store</h1>
<p>One loaf of demo bread. The seeded bug: the shipping hint labels the
total in CAD regardless of where the page runs; the agent is asked to
fix the label, then click Pay, which no accessibility tree can find.</p>

<div id="total" data-bug="currency">
  $<span id="amount">4.50</span>&nbsp;<span id="cur">CAD</span>
  <small>+ <span id="ship">$1.00 <span id="shipcur">CAD</span></span> shipping</small>
</div>

<canvas id="pay" width="220" height="72" role="button" aria-label="Pay"></canvas>
<p id="out" aria-live="polite"></p>

<script>
const c = document.getElementById('pay'), g = c.getContext('2d');
g.fillStyle = '#1a1a1a'; g.fillRect(0, 0, c.width, c.height);
g.fillStyle = '#fff'; g.font = '600 24px system-ui';
g.textAlign = 'center'; g.textBaseline = 'middle'; g.fillText('Pay', c.width/2, c.height/2);
c.addEventListener('click', () => {
  document.getElementById('out').textContent =
    (document.getElementById('cur').textContent === 'USD' ? 'paid $4.50 USD' : 'charged $4.50 CAD');
});
// The bug visually: leave the hints as-is; the demo's fix flips both
// to USD by a DOM change after the checkout bug report.
</script>
`

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("demo store on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
