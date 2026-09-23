// SPDX-License-Identifier: Apache-2.0






























package netresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)



type Channel string

const (



	ChannelEntryPoint Channel = "entry-point"

	ChannelDNS Channel = "dns"

	ChannelHosted Channel = "hosted"
)

const txtLabel = "_baton"





var ErrNotNamed = errors.New("netresolve: reference names no network")





var ErrRecordUnusable = errors.New("netresolve: the record names a network but cannot be used")




var ErrAmbiguous = errors.New("netresolve: several records; one domain names one network")





type Resolution struct {
	Ref        string
	Channel    Channel
	Candidates []string


	Pin   string
	Notes []string
}



func ChannelOf(ref string) Channel {













	if schemeRe.MatchString(ref) {
		return ChannelEntryPoint
	}
	if strings.Contains(ref, "@") {
		return ChannelHosted
	}
	return ChannelDNS
}


type TXT struct {
	V  string
	K  string
	D  string
	EP []string
}






func ParseTXT(txt string) TXT {
	var out TXT
	for _, part := range strings.Split(txt, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "v":
			out.V = val
		case "k":
			out.K = val
		case "d":
			out.D = val
		case "ep":
			for _, e := range strings.Split(val, ",") {
				if e = strings.TrimSpace(e); e != "" {
					out.EP = append(out.EP, strings.TrimRight(e, "/"))
				}
			}
		}
	}
	return out
}

var (

	schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)
	baton1   = regexp.MustCompile(`(^|;)\s*v=baton1(\s*;|$)`)
	fpRe   = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]+$`)
)



type Resolver struct {


	LookupTXT func(ctx context.Context, name string) ([]string, error)

	HTTP *http.Client
}

func (r *Resolver) lookupTXT(ctx context.Context, name string) ([]string, error) {
	if r.LookupTXT != nil {
		return r.LookupTXT(ctx, name)
	}
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (r *Resolver) http() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}






func (r *Resolver) Resolve(ctx context.Context, ref string) (Resolution, error) {
	switch ChannelOf(ref) {
	case ChannelEntryPoint:



		return Resolution{
			Ref: ref, Channel: ChannelEntryPoint,
			Candidates: []string{strings.TrimRight(ref, "/")},
			Notes:      []string{"channel 0: no key to pin; the entry point's key is learned on first contact"},
		}, nil
	case ChannelDNS:
		return r.resolveDNS(ctx, ref)
	default:
		return r.resolveHosted(ctx, ref)
	}
}

func (r *Resolver) resolveDNS(ctx context.Context, ref string) (Resolution, error) {
	name := txtLabel + "." + ref
	records, err := r.lookupTXT(ctx, name)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: no %s TXT record (%v)", ErrNotNamed, name, err)
	}
	var ours []string
	for _, t := range records {
		if baton1.MatchString(t) {
			ours = append(ours, t)
		}
	}
	if len(ours) == 0 {
		return Resolution{}, fmt.Errorf("%w: %s has TXT records, but none says v=baton1", ErrNotNamed, name)
	}
	if len(ours) > 1 {
		return Resolution{}, fmt.Errorf("%w: %s has %d v=baton1 records", ErrAmbiguous, name, len(ours))
	}
	rec := ParseTXT(ours[0])
	if !fpRe.MatchString(rec.K) {
		return Resolution{}, fmt.Errorf("%w: %s says v=baton1 but has no usable k= fingerprint", ErrRecordUnusable, name)
	}
	res := Resolution{Ref: ref, Channel: ChannelDNS, Pin: rec.K, Notes: []string{"read " + name}}
	res.Candidates = append(res.Candidates, rec.EP...)
	if rec.D != "" {
		res.Notes = append(res.Notes, "descriptor document at "+rec.D)
	}
	if len(res.Candidates) == 0 && rec.D == "" {
		return Resolution{}, fmt.Errorf("%w: %s says v=baton1 but names neither ep= nor d=", ErrRecordUnusable, name)
	}
	return res, nil
}




type hostedDoc struct {
	Identity struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"identity"`
	Endpoints []struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"endpoints"`
}

func (r *Resolver) resolveHosted(ctx context.Context, ref string) (Resolution, error) {
	name, registry, _ := strings.Cut(ref, "@")
	if name == "" || registry == "" {
		return Resolution{}, fmt.Errorf("%w: %q is not name@registry", ErrRecordUnusable, ref)
	}
	base := registry
	if !strings.HasPrefix(strings.ToLower(base), "http") {
		base = "https://" + base
	}
	url := strings.TrimRight(base, "/") + "/api/resolve/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: %v", ErrRecordUnusable, err)
	}
	resp, err := r.http().Do(req)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: %s did not answer (%v)", ErrNotNamed, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Resolution{}, fmt.Errorf("%w: %s answered %d", ErrNotNamed, url, resp.StatusCode)
	}
	var doc hostedDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return Resolution{}, fmt.Errorf("%w: %s answered something that is not a resolution (%v)", ErrRecordUnusable, url, err)
	}
	if !fpRe.MatchString(doc.Identity.Fingerprint) {
		return Resolution{}, fmt.Errorf("%w: %s named no usable key fingerprint", ErrRecordUnusable, url)
	}
	res := Resolution{Ref: ref, Channel: ChannelHosted, Pin: doc.Identity.Fingerprint, Notes: []string{"asked " + url}}
	for _, ep := range doc.Endpoints {
		proto := ep.Protocol
		if proto == "" {
			proto = "https"
		}
		res.Candidates = append(res.Candidates, fmt.Sprintf("%s://%s:%d", proto, ep.Address, ep.Port))
	}
	if len(res.Candidates) == 0 {
		return Resolution{}, fmt.Errorf("%w: %s named the network but no endpoint", ErrRecordUnusable, url)
	}
	return res, nil
}
