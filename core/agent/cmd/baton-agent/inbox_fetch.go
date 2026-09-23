// SPDX-License-Identifier: Apache-2.0

package main



















import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/batonos/baton/core/agent/internal/identity"
)






func runInboxFetch(args []string, dataDir string) int {
	if len(args) != 2 {
		fmt.Fprint(os.Stderr, "baton-agent inbox fetch: give a message id and an attachment index\n"+
			"  baton-inbox fetch msg_01a0… 0 > report.pdf\n"+
			"  The index is the position in the message's manifest; `inbox next --json` prints it.\n")
		return exitConfigError
	}
	messageID, idxRaw := args[0], args[1]
	idx, err := strconv.Atoi(idxRaw)
	if err != nil || idx < 0 {
		fmt.Fprintf(os.Stderr, "baton-agent inbox fetch: %q is not an attachment index\n"+
			"  Indexes start at 0 and are positions in the manifest, not names.\n", idxRaw)
		return exitConfigError
	}

	ids := identity.NewStore(dataDir)
	if !ids.Enrolled() {
		fmt.Fprint(os.Stderr, "baton-agent inbox fetch: this node holds no identity, so it cannot ask for anything\n"+
			"  Attachments are fetched over the node face with the node's own certificate.\n")
		return exitConfigError
	}
	id, err := ids.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-agent: read identity: %v\n", err)
		return exitInternal
	}
















	master := os.Getenv("BATON_MASTER_URL")
	if master == "" {
		master = id.EntryPoint
	}
	if master == "" {
		fmt.Fprint(os.Stderr, "baton-agent inbox fetch: this node has no control-plane address it can trust\n"+
			"  BATON_MASTER_URL is unset and no entry point was recorded at enrolment.\n"+
			"  Set BATON_MASTER_URL to the address this node reaches its master at.\n")
		return exitConfigError
	}

	client, err := nodeClient(ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-agent: %v\n", err)
		return exitInternal
	}
	url := fmt.Sprintf("%s/api/v1alpha1/agent/messages/%s/attachments/%d",
		strings.TrimRight(master, "/"), messageID, idx)
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-agent: reach the control plane: %v\n", err)
		return exitInternal
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return reportFetchRefusal(resp, messageID, idx)
	}



	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		fmt.Fprintf(os.Stderr, "%s  (%s)\n", cd, resp.Header.Get("Content-Type"))
	}
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {


		fmt.Fprintf(os.Stderr, "baton-agent: the transfer stopped part way: %v\n"+
			"  What was written is incomplete. Delete it and fetch again.\n", err)
		return exitInternal
	}
	return exitOK
}









func reportFetchRefusal(resp *http.Response, messageID string, idx int) int {
	return writeFetchRefusal(os.Stderr, resp, messageID, idx)
}








func writeFetchRefusal(w io.Writer, resp *http.Response, messageID string, idx int) int {
	var apiErr struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Remediation string `json:"remediation"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(body, &apiErr)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		fmt.Fprintf(w, "baton-agent inbox fetch: %s has no attachment %d\n"+
			"  `baton-inbox peek --json` prints the manifest; the index is a position in it.\n",
			messageID, idx)
		return exitConfigError
	case resp.StatusCode == http.StatusGone:
		fmt.Fprintf(w, "baton-agent inbox fetch: the bytes for %s attachment %d are gone\n"+
			"  The manifest is not wrong — the control plane no longer holds what it names.\n"+
			"  Ask the sender to send it again.\n", messageID, idx)
		return exitInternal
	default:
		msg := apiErr.Message
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		fmt.Fprintf(w, "baton-agent inbox fetch: the control plane refused (%d %s): %s\n",
			resp.StatusCode, apiErr.Code, msg)
		return exitInternal
	}
}








func nodeClient(ids *identity.Store) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(ids.CertPath(), ids.KeyPath())
	if err != nil {
		return nil, fmt.Errorf("load node certificate: %w", err)
	}
	caPEM, err := os.ReadFile(ids.CAPath())
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("cluster CA bundle is not valid PEM")
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      pool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}, nil
}
