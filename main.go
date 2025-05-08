package loops

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type PeriodicCallback func(ctx context.Context) error
type GracefulLoopCallback func(ctx context.Context, wg *sync.WaitGroup)

type GracefulOnStartParams struct {
	RunAsBackground []GracefulLoopCallback
}

type GracefulLoopParams struct {
	OnStart      GracefulOnStartParams
	OnCTXCancel  func()
	OnJobsFinish func()
}

func NewGracefulLoop(ctx context.Context, params GracefulLoopParams) {
	var wg sync.WaitGroup
	stop := make(chan os.Signal, 1)

	// Set os cancel signals
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, callback := range params.OnStart.RunAsBackground {
		if callback != nil {
			go callback(ctx, &wg) // run as background
		}
	}

	// Wait signal
	<-stop

	cancel()
	if params.OnCTXCancel != nil {
		go params.OnCTXCancel()
	}

	wg.Wait()
	if params.OnJobsFinish != nil {
		params.OnJobsFinish()
	}
}

// Params holds configuration for the periodic loop
type PeriodicParams struct {
	Callback       PeriodicCallback // The callback function to execute
	ErrorFlag      string
	Sleep          time.Duration // Interval between executions
	StartTime      string        // Optional start time (e.g., "00:00")
	RunImmediately bool          // Whether to run the callback immediately
}

// NewPeriodicLoop runs a periodic callback with configuration from Params
func NewPeriodicLoop(ctx context.Context, wg *sync.WaitGroup, now time.Time, params PeriodicParams) {
	wg.Add(1)
	defer wg.Done()

	// Execute the callback with panic handling
	loopCallback := func() {
		wg.Add(1)
		defer wg.Done() // Ensure wg.Done() is called even if callback panics
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Callback panicked: %v", r)
			}
		}()
		if err := params.Callback(ctx); err != nil {
			if params.ErrorFlag != "" {
				log.Printf("[%s] Callback error: %v", params.ErrorFlag, err)
			} else {
				log.Printf("Callback error: %v", err)
			}
			if ctx.Err() != nil {
				log.Printf("Context cancelled, stopping: %v", ctx.Err())
				return
			}
			time.Sleep(10 * time.Second)
			return // Return to continue the loop
		}
	}

	// If RunImmediately is true, execute the callback once immediately
	if params.RunImmediately {
		loopCallback()
	}

	// If StartTime is specified (e.g., "00:00"), wait until that time to start the loop
	if params.StartTime != "" {
		// Parse the start time (format: "HH:MM")
		t, err := time.Parse("15:04", params.StartTime)
		if err != nil {
			log.Printf("Invalid start time format (%s): %v. Starting immediately.", params.StartTime, err)
		} else {
			// Calculate the time until the next occurrence of StartTime
			start := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
			if now.After(start) {
				// If the time is already past for today, schedule for tomorrow
				start = start.Add(24 * time.Hour)
			}
			waitDuration := start.Sub(now)
			// Wait until the start time or context cancellation
			select {
			case <-time.After(waitDuration):
				// Start the loop
			case <-ctx.Done():
				log.Printf("Periodic loop stopped before starting: %v", ctx.Err())
				return
			}
		}
	}

	// Main periodic loop
	for {
		// Check if context was canceled
		select {
		case <-ctx.Done():
			log.Printf("Periodic loop stopped: %v", ctx.Err())
			return
		default:
			// Continue
		}

		loopCallback()

		// Wait for the next iteration
		timeout := time.After(params.Sleep)
		select {
		case <-ctx.Done():
			log.Printf("Periodic loop stopped: %v", ctx.Err())
			return
		case <-timeout:
			// Continue to the next iteration
			continue
		}
	}
}

func NewRetryWithContext(ctx context.Context, maxRetries int, delay time.Duration, fn func() error) error {
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = fn()
		if err == nil {
			return nil // Successed
		}

		if ctx.Err() != nil {
			return ctx.Err() // Context was canceled
		}

		if attempt < maxRetries {
			time.Sleep(delay)
		}
	}

	return errors.New("operation failed after retries: " + err.Error())
}
