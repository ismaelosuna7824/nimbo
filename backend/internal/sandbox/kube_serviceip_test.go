package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// rangeFull is what a k3s 1.36 API server answers while its ServiceCIDR
// allocators start.
func rangeFull(name string) error {
	return apierrors.NewInternalError(fmt.Errorf("failed to allocate a serviceIP for Service %q: range is full", name))
}

func TestWaitServiceIPsWaitsForKubernetesClusterIP(t *testing.T) {
	cs := fake.NewClientset()
	gets := 0
	// The API server answers, the kubernetes Service first is missing, then
	// exists without a ClusterIP, then gets one.
	cs.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}}
		switch {
		case gets == 1:
			return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "kubernetes")
		case gets >= 3:
			svc.Spec.ClusterIP = "10.43.0.1"
		}
		return true, svc, nil
	})
	kc := &kubeClient{cs: cs}
	if err := kc.waitServiceIPs(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if gets != 3 {
		t.Errorf("%d gets, want 3", gets)
	}
}

func TestWaitServiceIPsTimesOut(t *testing.T) {
	kc := &kubeClient{cs: fake.NewClientset()}
	err := kc.waitServiceIPs(context.Background(), 600*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "could not allocate Service IPs") {
		t.Fatalf("err %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := kc.waitServiceIPs(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestApplyWithRetry(t *testing.T) {
	ctx := context.Background()
	// Transient: retried until it passes.
	calls := 0
	err := applyWithRetry(ctx, 5*time.Second, func() error {
		if calls++; calls < 3 {
			return rangeFull("backend")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("transient: err %v after %d calls", err, calls)
	}
	// Other errors, including other internal errors, are not retried.
	for _, want := range []error{
		apierrors.NewInvalid(corev1.SchemeGroupVersion.WithKind("Service").GroupKind(), "backend", nil),
		apierrors.NewInternalError(errors.New("etcdserver: request timed out")),
		errors.New("failed to allocate a serviceIP: range is full"), // not an API status
	} {
		calls = 0
		if err := applyWithRetry(ctx, 5*time.Second, func() error { calls++; return want }); err != want || calls != 1 {
			t.Errorf("%v: err %v after %d calls", want, err, calls)
		}
	}
	// A range that stays full fails after the limit with the API error.
	start := time.Now()
	calls = 0
	err = applyWithRetry(ctx, 700*time.Millisecond, func() error { calls++; return rangeFull("backend") })
	if !serviceIPNotReady(err) || calls < 2 || time.Since(start) > 2*time.Second {
		t.Errorf("persistent: err %v after %d calls in %s", err, calls, time.Since(start))
	}
	// Cancellation stops the retries.
	cctx, cancel := context.WithCancel(ctx)
	calls = 0
	err = applyWithRetry(cctx, time.Minute, func() error { calls++; cancel(); return rangeFull("backend") })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("canceled: err %v after %d calls", err, calls)
	}
}
