package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"dnsbench/internal/ui"
)

func newIntroCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "intro",
		Short: "Learn what dnsbench measures and how to read the results",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			out := cmd.OutOrStdout()
			for _, s := range introSections() {
				fmt.Fprintln(out, ui.Bold(s.title))
				fmt.Fprintln(out, s.body)
				fmt.Fprintln(out)
			}
		},
	}
}

type introSection struct {
	title string
	body  string
}

func introSections() []introSection {
	return []introSection{
		{
			"What is DNS?",
			`DNS (Domain Name System) is the phone book of the internet. Every time you
open a website, your device asks a DNS server to translate a name such as
example.com into an IP address it can connect to. A recursive DNS server does
that lookup on your behalf, walking the chain of authoritative servers and
caching the answers it finds. Almost every online action starts with one or
more DNS lookups, so a slow or unreliable resolver adds delay to everything.`,
		},
		{
			"What does this test measure?",
			`dnsbench measures several independent things about each DNS server:

  - Cached latency: how fast the server answers for popular names it almost
    certainly already has in its cache. This is the answer speed you feel most
    often in daily browsing.
  - Uncached latency: how fast the server resolves names guaranteed not to be
    in its cache (this requires a zone you control, so each query is a real
    cache miss).
  - Recursive-path latency: how fast the server walks the TLD infrastructure
    for random names, exercising its full recursion path.
  - Loss and stability: how many queries go unanswered and how much the
    latency jitters between consecutive queries.
  - DNSSEC validation: whether the server cryptographically validates signed
    answers and rejects forged ones.
  - NXDOMAIN handling: whether the server honestly reports that a name does
    not exist, or intercepts the error and redirects you to ads or a search
    page.
  - Rebinding protection: whether the server blocks public names that resolve
    to private addresses, a trick used to attack devices inside your network.`,
		},
		{
			"Why should the network be idle during the test?",
			`DNS queries are tiny, so any competing traffic distorts the measurement.
A download, a video stream or a video call fills the link and its buffers,
inflating latency and causing spurious loss that gets blamed on the DNS
server. For trustworthy numbers, run the test while nobody is using the
network for anything else.`,
		},
		{
			"Why do results depend on location, ISP and time of day?",
			`Public DNS providers run many datacenters, and your queries reach a
different one depending on where you are and how your ISP routes traffic.
Peering agreements, congestion and cache warmth all differ per network and
per hour. The evening peak can double latencies that look great at 4 a.m.
That is why dnsbench results are a snapshot of this network, this ISP, this
location and this moment, and why you should re-run the test at the times of
day you actually use the internet.`,
		},
		{
			"Why does lower DNS latency not mean faster internet overall?",
			`DNS is only the first step of a connection. After the name is resolved,
your browser still has to establish TCP and TLS connections and download the
actual content, which usually takes hundreds of times longer than the lookup.
Cached answers are also reused for minutes or hours, so most page loads skip
DNS entirely. Shaving 20 ms off DNS helps first visits feel snappier, but it
does not increase bandwidth or make downloads faster. Reliability and honest
behavior are usually worth more than a few milliseconds of raw speed.`,
		},
	}
}
