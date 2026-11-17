package audit

import (
	"context"
	"errors"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type LocationEntry struct {
	Node string `json:"node"`
	Unit string `json:"unit"`
	Role string `json:"role"`
}

type Location struct {
	HazardID string          `json:"hazard_id"`
	Entries  []LocationEntry `json:"locations"`
	Partial  bool            `json:"partial"`
}

type locationReader interface {
	AuditLocation(context.Context, string) (*store.AuditLocationResult, error)
}

type nodeLookup interface {
	NodeForPod(context.Context, string) (string, error)
}

type locationService struct {
	reader locationReader
	nodes  nodeLookup
}

// NewKubernetesLocator resolves Mongo replica pod names to the node names printed by
// `kubectl get nodes`, as required by the Audit API contract.
func NewKubernetesLocator(reader locationReader, namespace string) (Locator, error) {
	if namespace == "" {
		return nil, errors.New("pod namespace is empty")
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}
	return &locationService{reader: reader, nodes: &kubernetesNodeLookup{client: client, namespace: namespace}}, nil
}

type unavailableLocator struct {
	err error
}

func NewUnavailableLocator(err error) Locator {
	return unavailableLocator{err: err}
}

func (l unavailableLocator) Locate(context.Context, string) (*Location, error) {
	return nil, l.err
}

func (s *locationService) Locate(ctx context.Context, hazardID string) (*Location, error) {
	result, err := s.reader.AuditLocation(ctx, hazardID)
	if err != nil || result == nil {
		return nil, err
	}

	replicas := slices.Clone(result.Replicas)
	slices.SortFunc(replicas, func(left, right store.AuditReplica) int {
		if left.Role != right.Role {
			if left.Role == "primary" {
				return -1
			}
			if right.Role == "primary" {
				return 1
			}
		}
		if left.Pod < right.Pod {
			return -1
		}
		if left.Pod > right.Pod {
			return 1
		}
		return 0
	})

	response := &Location{HazardID: result.HazardID, Entries: []LocationEntry{}, Partial: result.Partial}
	replicaNumber := 0
	for _, replica := range replicas {
		node, err := s.nodes.NodeForPod(ctx, replica.Pod)
		if err != nil {
			response.Partial = true
			continue
		}
		unit := result.Shard
		if replica.Role != "primary" {
			replicaNumber++
			unit = fmt.Sprintf("%s-r%d", result.Shard, replicaNumber)
		}
		response.Entries = append(response.Entries, LocationEntry{Node: node, Unit: unit, Role: replica.Role})
	}
	if len(response.Entries) == 0 {
		return nil, errors.New("no replica pod could be resolved to a Kubernetes node")
	}
	return response, nil
}

type kubernetesNodeLookup struct {
	client    kubernetes.Interface
	namespace string
}

func (l *kubernetesNodeLookup) NodeForPod(ctx context.Context, podName string) (string, error) {
	pod, err := l.client.CoreV1().Pods(l.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s: %w", podName, err)
	}
	if pod.Spec.NodeName == "" {
		return "", fmt.Errorf("pod %s is not scheduled", podName)
	}
	return pod.Spec.NodeName, nil
}
