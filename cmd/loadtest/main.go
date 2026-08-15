package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

const (
	defaultCorporateAccountID = "19c5d2b61ac27dd55d9c9daff5af441"
	defaultCardAccountID      = "19c5d2b61ac27dd55d9c9daff5af442"
	defaultAuthorizationID    = "19c5d2b61ac27dd55d9c9daff5af445"
	postedConcurrency         = 256
	riskConcurrency           = 4_096
)

type options struct {
	baseURL         string
	operation       string
	accountID       string
	cardID          string
	authorizationID string
	merchantID      string
	amountCents     uint64
	requests        int
	concurrency     int
	runs            int
	warmup          int
	timeout         time.Duration
}

type result struct {
	duration   time.Duration
	status     string
	httpStatus int
	err        error
}

type summary struct {
	Run            int            `json:"run,omitempty"`
	Operation      string         `json:"operation"`
	Requests       int            `json:"requests"`
	Concurrency    int            `json:"concurrency"`
	WarmupRequests int            `json:"warmup_requests"`
	Successful     int            `json:"successful"`
	Failed         int            `json:"failed"`
	DurationMS     int64          `json:"duration_ms"`
	RequestsPerSec float64        `json:"requests_per_second"`
	P50MS          float64        `json:"p50_ms"`
	P95MS          float64        `json:"p95_ms"`
	P99MS          float64        `json:"p99_ms"`
	MaxMS          float64        `json:"max_ms"`
	FailureReasons map[string]int `json:"failure_reasons,omitempty"`
	SampleError    string         `json:"sample_error,omitempty"`
}

func main() {
	configuration := parseFlags()
	if err := validate(configuration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = configuration.concurrency
	transport.MaxIdleConnsPerHost = configuration.concurrency
	client := &http.Client{Transport: transport, Timeout: configuration.timeout}
	defer transport.CloseIdleConnections()

	if configuration.warmup > 0 {
		warmupConfiguration := configuration
		warmupConfiguration.requests = configuration.warmup
		warmup := benchmark(client, warmupConfiguration)
		if warmup.Failed != 0 {
			fmt.Fprintf(os.Stderr, "warm-up failed: %d of %d requests failed (%v): %s\n",
				warmup.Failed, warmup.Requests, warmup.FailureReasons, warmup.SampleError)
			os.Exit(1)
		}
	}

	reports := make([]summary, 0, configuration.runs)
	failed := false
	for run := 1; run <= configuration.runs; run++ {
		report := benchmark(client, configuration)
		if configuration.runs > 1 {
			report.Run = run
		}
		reports = append(reports, report)
		failed = failed || report.Failed != 0
	}

	var output any = reports[0]
	if len(reports) > 1 {
		output = reports
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if failed {
		os.Exit(1)
	}
}

func benchmark(client *http.Client, configuration options) summary {
	jobs := make(chan struct{})
	results := make(chan result, configuration.concurrency)
	var workers sync.WaitGroup
	for range configuration.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				results <- execute(client, configuration)
			}
		}()
	}

	started := time.Now()
	go func() {
		for range configuration.requests {
			jobs <- struct{}{}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	latencies := make([]time.Duration, 0, configuration.requests)
	successful := 0
	failureReasons := make(map[string]int)
	sampleError := ""
	for result := range results {
		latencies = append(latencies, result.duration)
		if result.err == nil && (result.status == "posted" || result.status == "approved") {
			successful++
			continue
		}
		reason := failureReason(result)
		failureReasons[reason]++
		if sampleError == "" && result.err != nil {
			sampleError = result.err.Error()
		}
	}
	elapsed := time.Since(started)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	return summary{
		Operation:      configuration.operation,
		Requests:       configuration.requests,
		Concurrency:    configuration.concurrency,
		WarmupRequests: configuration.warmup,
		Successful:     successful,
		Failed:         configuration.requests - successful,
		DurationMS:     elapsed.Milliseconds(),
		RequestsPerSec: float64(configuration.requests) / elapsed.Seconds(),
		P50MS:          percentileMS(latencies, 0.50),
		P95MS:          percentileMS(latencies, 0.95),
		P99MS:          percentileMS(latencies, 0.99),
		MaxMS:          percentileMS(latencies, 1),
		FailureReasons: failureReasons,
		SampleError:    sampleError,
	}
}

func parseFlags() options {
	var configuration options
	flag.StringVar(&configuration.baseURL, "url", "http://127.0.0.1:8080", "ledger-service base URL")
	flag.StringVar(&configuration.operation, "operation", "authorize", "operation: authorize, increment, topup, or withdraw")
	flag.StringVar(&configuration.accountID, "account-id", defaultCorporateAccountID, "corporate wallet account ID for topup or withdraw")
	flag.StringVar(&configuration.cardID, "card-id", defaultCardAccountID, "card wallet account ID for authorize")
	flag.StringVar(&configuration.authorizationID, "authorization-id", defaultAuthorizationID, "original authorization ID for increment")
	flag.StringVar(&configuration.merchantID, "merchant-id", "MRC_009", "merchant ID for authorize")
	flag.Uint64Var(&configuration.amountCents, "amount-cents", 1, "amount per request")
	flag.IntVar(&configuration.requests, "requests", 10_000, "total requests")
	flag.IntVar(&configuration.concurrency, "concurrency", 0, "concurrent workers; 0 selects 256 for posted operations or 4096 for risk-evaluated operations")
	flag.IntVar(&configuration.runs, "runs", 3, "number of measured runs using one HTTP transport")
	flag.IntVar(&configuration.warmup, "warmup", 1_000, "unmeasured warm-up requests")
	flag.DurationVar(&configuration.timeout, "timeout", 15*time.Second, "per-request HTTP timeout")
	flag.Parse()
	if configuration.concurrency == 0 {
		configuration.concurrency = defaultConcurrency(configuration.operation)
	}
	return configuration
}

func defaultConcurrency(operation string) int {
	if operation == "authorize" || operation == "increment" {
		return riskConcurrency
	}
	return postedConcurrency
}

func validate(configuration options) error {
	if configuration.operation != "authorize" && configuration.operation != "increment" &&
		configuration.operation != "topup" && configuration.operation != "withdraw" {
		return fmt.Errorf("operation must be authorize, increment, topup, or withdraw")
	}
	if configuration.operation == "authorize" && configuration.cardID == "" {
		return fmt.Errorf("card-id is required for authorize")
	}
	if (configuration.operation == "topup" || configuration.operation == "withdraw") && configuration.accountID == "" {
		return fmt.Errorf("account-id is required for topup and withdraw")
	}
	if configuration.operation == "increment" && configuration.authorizationID == "" {
		return fmt.Errorf("authorization-id is required for increment")
	}
	if configuration.amountCents == 0 || configuration.requests <= 0 || configuration.concurrency <= 0 || configuration.runs <= 0 {
		return fmt.Errorf("amount-cents, requests, concurrency, and runs must be positive")
	}
	if configuration.warmup < 0 {
		return fmt.Errorf("warmup must not be negative")
	}
	return nil
}

func execute(client *http.Client, configuration options) result {
	requestID := tb.ID().String()
	path := "/v1/authorizations"
	var payload any = map[string]any{
		"request_id":   requestID,
		"card_id":      configuration.cardID,
		"merchant_id":  configuration.merchantID,
		"amount_cents": configuration.amountCents,
	}
	switch configuration.operation {
	case "increment":
		path = "/v1/authorizations/" + configuration.authorizationID + "/increments"
		payload = map[string]any{
			"request_id":      requestID,
			"increment_cents": configuration.amountCents,
		}
	case "topup":
		path = "/v1/topups"
		payload = map[string]any{
			"request_id":   requestID,
			"account_id":   configuration.accountID,
			"amount_cents": configuration.amountCents,
		}
	case "withdraw":
		path = "/v1/withdrawals"
		payload = map[string]any{
			"request_id":   requestID,
			"account_id":   configuration.accountID,
			"amount_cents": configuration.amountCents,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return result{err: err}
	}

	request, err := http.NewRequest(http.MethodPost, configuration.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return result{err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return result{duration: time.Since(started), err: err}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	measured := result{duration: time.Since(started), httpStatus: response.StatusCode, err: err}
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		if err == nil {
			measured.err = fmt.Errorf("HTTP %d", response.StatusCode)
		}
		return measured
	}
	var outcome struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(responseBody, &outcome); err != nil {
		measured.err = err
		return measured
	}
	measured.status = outcome.Status
	return measured
}

func failureReason(measured result) string {
	if measured.err != nil {
		if measured.httpStatus != 0 && (measured.httpStatus < 200 || measured.httpStatus >= 300) {
			return fmt.Sprintf("http_%d", measured.httpStatus)
		}
		return "request_error"
	}
	if measured.status == "" {
		return "missing_outcome"
	}
	return "outcome_" + measured.status
}

func percentileMS(values []time.Duration, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * percentile)
	return float64(values[index].Microseconds()) / 1000
}
