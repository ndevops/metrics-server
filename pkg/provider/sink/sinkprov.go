// Copyright 2018 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sink

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	metrics "k8s.io/metrics/pkg/apis/metrics"

	"github.com/kubernetes-incubator/metrics-server/pkg/provider"
	"github.com/kubernetes-incubator/metrics-server/pkg/sink"
	"github.com/kubernetes-incubator/metrics-server/pkg/sources"
)

// sinkMetricsProvider is a provider.MetricsProvider that also acts as a sink.MetricSink
type sinkMetricsProvider struct {
	mu    sync.RWMutex
	nodes map[string]sources.NodeMetricsPoint
	pods  map[apitypes.NamespacedName]sources.PodMetricsPoint
}

// NewSinkProvider returns a MetricSink that feeds into a MetricsProvider.
func NewSinkProvider() (sink.MetricSink, provider.MetricsProvider) {
	prov := &sinkMetricsProvider{}
	return prov, prov
}

func (p *sinkMetricsProvider) GetNodeMetrics(nodes ...string) ([]time.Time, []corev1.ResourceList, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	timestamps := make([]time.Time, len(nodes))
	resMetrics := make([]corev1.ResourceList, len(nodes))

	for i, node := range nodes {
		metricPoint, present := p.nodes[node]
		if !present {
			continue
		}

		timestamps[i] = metricPoint.Timestamp
		resMetrics[i] = corev1.ResourceList{
			corev1.ResourceName(corev1.ResourceCPU):    metricPoint.CpuUsage,
			corev1.ResourceName(corev1.ResourceMemory): metricPoint.MemoryUsage,
		}
	}

	return timestamps, resMetrics, nil
}

func (p *sinkMetricsProvider) GetContainerMetrics(pods ...apitypes.NamespacedName) ([]time.Time, [][]metrics.ContainerMetrics, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	timestamps := make([]time.Time, len(pods))
	resMetrics := make([][]metrics.ContainerMetrics, len(pods))

	for i, pod := range pods {
		metricPoint, present := p.pods[pod]
		if !present {
			continue
		}

		contMetrics := make([]metrics.ContainerMetrics, len(metricPoint.Containers))
		var earliestTS *time.Time
		for i, contPoint := range metricPoint.Containers {
			contMetrics[i] = metrics.ContainerMetrics{
				Name: contPoint.Name,
				Usage: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceCPU):    contPoint.CpuUsage,
					corev1.ResourceName(corev1.ResourceMemory): contPoint.MemoryUsage,
				},
			}
			if earliestTS == nil || earliestTS.After(contPoint.Timestamp) {
				ts := contPoint.Timestamp // copy to avoid loop iteration variable issues
				earliestTS = &ts
			}
		}
		if earliestTS == nil {
			// we had no containers
			timestamps[i] = time.Time{}
		} else {
			timestamps[i] = *earliestTS
		}
		resMetrics[i] = contMetrics
	}
	return timestamps, resMetrics, nil
}

func (p *sinkMetricsProvider) Receive(batch *sources.MetricsBatch) error {
	newNodes := make(map[string]sources.NodeMetricsPoint, len(batch.Nodes))
	for _, nodePoint := range batch.Nodes {
		if _, exists := newNodes[nodePoint.Name]; exists {
			return fmt.Errorf("duplicate node %s received", nodePoint.Name)
		}
		newNodes[nodePoint.Name] = nodePoint
	}

	newPods := make(map[apitypes.NamespacedName]sources.PodMetricsPoint, len(batch.Pods))
	for _, podPoint := range batch.Pods {
		podIdent := apitypes.NamespacedName{Name: podPoint.Name, Namespace: podPoint.Namespace}
		if _, exists := newPods[podIdent]; exists {
			return fmt.Errorf("duplicate pod %s received", podIdent)
		}
		newPods[podIdent] = podPoint
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.nodes = newNodes
	p.pods = newPods

	return nil
}
