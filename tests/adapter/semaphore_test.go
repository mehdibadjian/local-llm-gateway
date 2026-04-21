package adapter_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/adapter"
)

func TestSemaphore_AcquireLLM_BlocksEmbed(t *testing.T) {
	s := adapter.NewGlobalResourceSemaphore()
	ctx := context.Background()

	// Acquire LLM (weight=2), fills the semaphore completely.
	if err := s.AcquireLLM(ctx); err != nil {
		t.Fatalf("AcquireLLM: %v", err)
	}
	defer s.ReleaseLLM()

	// AcquireEmbed must block because weight=2 is already held.
	done := make(chan error, 1)
	go func() {
		ctxTimeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		done <- s.AcquireEmbed(ctxTimeout)
	}()

	err := <-done
	if err == nil {
		t.Fatal("AcquireEmbed should have been blocked and timed out, but it succeeded")
	}
}

func TestSemaphore_TwoEmbeds_DoNotBlock(t *testing.T) {
	s := adapter.NewGlobalResourceSemaphore()
	ctx := context.Background()

	// First embed (weight=1).
	if err := s.AcquireEmbed(ctx); err != nil {
		t.Fatalf("first AcquireEmbed: %v", err)
	}
	defer s.ReleaseEmbed()

	// Second embed (weight=1) must succeed immediately — total weight=2 fits within maxWeight=2.
	done := make(chan error, 1)
	go func() {
		ctxTimeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		err := s.AcquireEmbed(ctxTimeout)
		if err == nil {
			s.ReleaseEmbed()
		}
		done <- err
	}()

	if err := <-done; err != nil {
		t.Fatalf("second AcquireEmbed should not block, got: %v", err)
	}
}

func TestSemaphore_TwoLLMs_SecondBlocks(t *testing.T) {
	s := adapter.NewGlobalResourceSemaphore()
	ctx := context.Background()

	// First LLM acquire (weight=2).
	if err := s.AcquireLLM(ctx); err != nil {
		t.Fatalf("first AcquireLLM: %v", err)
	}
	defer s.ReleaseLLM()

	// Second LLM must block — would require weight=4, exceeding maxWeight=2.
	done := make(chan error, 1)
	go func() {
		ctxTimeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		done <- s.AcquireLLM(ctxTimeout)
	}()

	if err := <-done; err == nil {
		t.Fatal("second AcquireLLM should have been blocked, but it succeeded")
	}
}

func TestSemaphore_Release_UnblocksWaiter(t *testing.T) {
	s := adapter.NewGlobalResourceSemaphore()
	ctx := context.Background()

	if err := s.AcquireLLM(ctx); err != nil {
		t.Fatalf("AcquireLLM: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	acquired := make(chan struct{})
	go func() {
		defer wg.Done()
		if err := s.AcquireEmbed(ctx); err != nil {
			t.Errorf("AcquireEmbed after release: %v", err)
			return
		}
		close(acquired)
		s.ReleaseEmbed()
	}()

	// Give goroutine time to block on the semaphore.
	time.Sleep(20 * time.Millisecond)
	s.ReleaseLLM()

	select {
	case <-acquired:
		// success
	case <-time.After(200 * time.Millisecond):
		t.Fatal("AcquireEmbed did not unblock after ReleaseLLM")
	}
	wg.Wait()
}

func TestSemaphore_TryAcquireLLM_ReturnsFalseWhenFull(t *testing.T) {
	s := adapter.NewGlobalResourceSemaphore()
	ctx := context.Background()

	if err := s.AcquireLLM(ctx); err != nil {
		t.Fatalf("AcquireLLM: %v", err)
	}
	defer s.ReleaseLLM()

	if s.TryAcquireLLM() {
		t.Fatal("TryAcquireLLM should return false when semaphore is full")
	}
}
