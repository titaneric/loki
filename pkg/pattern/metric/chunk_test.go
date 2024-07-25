package metric

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/go-kit/log"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/grafana/loki/v3/pkg/detection"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/grafana/loki/v3/pkg/pattern/iter"
)

func TestForTypeAndRange(t *testing.T) {
	testCases := []struct {
		name       string
		c          *Chunk
		metricType Type
		start      model.Time
		end        model.Time
		expected   []logproto.Sample
	}{
		{
			name:       "Empty count",
			c:          &Chunk{},
			metricType: Count,
			start:      1,
			end:        10,
			expected:   []logproto.Sample{},
		},
		{
			name:       "Empty bytes",
			c:          &Chunk{},
			metricType: Bytes,
			start:      1,
			end:        10,
			expected:   []logproto.Sample{},
		},
		{
			name: "No Overlap -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      10,
			end:        20,
			expected:   []logproto.Sample{},
		},
		{
			name: "No Overlap -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      10,
			end:        20,
			expected:   []logproto.Sample{},
		},
		{
			name: "Complete Overlap -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      0,
			end:        10,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
				{Timestamp: 6 * 1e6, Value: 6},
			},
		},
		{
			name: "Complete Overlap -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      0,
			end:        10,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
				{Timestamp: 6 * 1e6, Value: 6},
			},
		},
		{
			name: "Partial Overlap -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      2,
			end:        5,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "Partial Overlap -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      2,
			end:        5,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "Single Element in Range -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      4,
			end:        5,
			expected:   []logproto.Sample{{Timestamp: 4 * 1e6, Value: 4}},
		},
		{
			name: "Single Element in Range -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      4,
			end:        5,
			expected:   []logproto.Sample{{Timestamp: 4 * 1e6, Value: 4}},
		},
		{
			name: "Start Before First Element -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      0,
			end:        5,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "Start Before First Element -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      0,
			end:        5,
			expected: []logproto.Sample{
				{Timestamp: 2 * 1e6, Value: 2},
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "End After Last Element -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      5,
			end:        10,
			expected: []logproto.Sample{
				{Timestamp: 6 * 1e6, Value: 6},
			},
		},
		{
			name: "End After Last Element -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      5,
			end:        10,
			expected: []logproto.Sample{
				{Timestamp: 6 * 1e6, Value: 6},
			},
		},
		{
			name: "End Exclusive -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      4,
			end:        6,
			expected: []logproto.Sample{
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "End Exclusive -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      4,
			end:        6,
			expected: []logproto.Sample{
				{Timestamp: 4 * 1e6, Value: 4},
			},
		},
		{
			name: "Start before First and End Inclusive of First Element -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      0,
			end:        3,
			expected:   []logproto.Sample{{Timestamp: 2 * 1e6, Value: 2}},
		},
		{
			name: "Start before First and End Inclusive of First Element -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      0,
			end:        3,
			expected:   []logproto.Sample{{Timestamp: 2 * 1e6, Value: 2}},
		},
		{
			name: "Start and End before First Element -- count",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Count: 2},
				{Timestamp: 4, Count: 4},
				{Timestamp: 6, Count: 6},
			}},
			metricType: Count,
			start:      0,
			end:        1,
			expected:   []logproto.Sample{},
		},
		{
			name: "Start and End before First Element -- bytes",
			c: &Chunk{Samples: Samples{
				{Timestamp: 2, Bytes: 2},
				{Timestamp: 4, Bytes: 4},
				{Timestamp: 6, Bytes: 6},
			}},
			metricType: Bytes,
			start:      0,
			end:        1,
			expected:   []logproto.Sample{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.c.ForTypeAndRange(tc.metricType, tc.start, tc.end)
			require.NoError(t, err)
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func Test_Chunks_Iterator(t *testing.T) {
	lbls := labels.Labels{
		labels.Label{Name: "foo", Value: "bar"},
		labels.Label{Name: "container", Value: "jar"},
	}
	chunks := NewChunks(lbls, NewChunkMetrics(nil, "test"), log.NewNopLogger())
	chunks.chunks = []*Chunk{
		{
			Samples: []Sample{
				{Timestamp: 2, Bytes: 2, Count: 1},
				{Timestamp: 4, Bytes: 4, Count: 3},
				{Timestamp: 6, Bytes: 6, Count: 5},
			},
			mint: 2,
			maxt: 6,
		},
	}

	t.Run("without grouping", func(t *testing.T) {
		it, err := chunks.Iterator(Bytes, nil, 0, 10, 2)
		require.NoError(t, err)

		res, err := iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, lbls.String(), res.Series[0].GetLabels())

		it, err = chunks.Iterator(Count, nil, 0, 10, 2)
		require.NoError(t, err)

		res, err = iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, lbls.String(), res.Series[0].GetLabels())
	})

	t.Run("grouping", func(t *testing.T) {
		grouping := &syntax.Grouping{
			Groups:  []string{"container"},
			Without: false,
		}

		expectedLabels := labels.Labels{
			labels.Label{
				Name:  "container",
				Value: "jar",
			},
		}

		it, err := chunks.Iterator(Bytes, grouping, 0, 10, 2)
		require.NoError(t, err)

		res, err := iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, expectedLabels.String(), res.Series[0].GetLabels())

		it, err = chunks.Iterator(Count, grouping, 0, 10, 2)
		require.NoError(t, err)

		res, err = iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, expectedLabels.String(), res.Series[0].GetLabels())
	})

	t.Run("grouping by a missing label", func(t *testing.T) {
		grouping := &syntax.Grouping{
			Groups:  []string{"missing"},
			Without: false,
		}

		expectedLabels := labels.Labels{
			labels.Label{
				Name:  "missing",
				Value: "",
			},
		}

		it, err := chunks.Iterator(Bytes, grouping, 0, 10, 2)
		require.NoError(t, err)

		res, err := iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, expectedLabels.String(), res.Series[0].GetLabels())

		it, err = chunks.Iterator(Count, grouping, 0, 10, 2)
		require.NoError(t, err)

		res, err = iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, expectedLabels.String(), res.Series[0].GetLabels())
	})

	t.Run("handle slice capacity out of range", func(t *testing.T) {
		chunks := NewChunks(lbls, NewChunkMetrics(nil, "test"), log.NewNopLogger())
		chunks.chunks = []*Chunk{
			{
				Samples: []Sample{},
			},
		}

		it, err := chunks.Iterator(Bytes, nil, 5e4, 0, 1e4)
		require.NoError(t, err)

		res, err := iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 0, len(res.Series))

		it, err = chunks.Iterator(Count, nil, 5e4, 0, 1e4)
		require.NoError(t, err)

		res, err = iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 0, len(res.Series))
	})

	t.Run("correctly sets capacity for samples slice to range / step", func(t *testing.T) {
		it, err := chunks.Iterator(Bytes, nil, 0, 10, 2)
		require.NoError(t, err)

		res, err := iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, lbls.String(), res.Series[0].GetLabels())
		require.Equal(t, 3, len(res.Series[0].Samples))
		require.Equal(t, 4, cap(res.Series[0].Samples))

		it, err = chunks.Iterator(Count, nil, 0, 10, 2)
		require.NoError(t, err)

		res, err = iter.ReadAllSamples(it)
		require.NoError(t, err)

		require.Equal(t, 1, len(res.Series))
		require.Equal(t, lbls.String(), res.Series[0].GetLabels())
		require.Equal(t, 3, len(res.Series[0].Samples))
		require.Equal(t, 4, cap(res.Series[0].Samples))
	})
}

func TestDownsample(t *testing.T) {
	mockWriter := &mockEntryWriter{}

	mockWriter.On("WriteEntry", mock.Anything, mock.Anything, mock.Anything)

	// Create a Chunks object with two rawChunks, each containing two Samples
	c := NewChunks(labels.Labels{
		labels.Label{Name: "foo", Value: "bar"},
		labels.Label{Name: "service_name", Value: "foo_service"},
		labels.Label{Name: "level", Value: "info"},
	}, NewChunkMetrics(nil, "test"), log.NewNopLogger())

	c.Observe(2, 1)
	c.Observe(2, 1)
	c.Observe(2, 1)

	now := model.Time(5000)
	// Call the Downsample function
	c.Downsample(now, mockWriter)

	lbls := labels.Labels{
		labels.Label{Name: detection.AggregatedMetricLabel, Value: "foo_service"},
		labels.Label{Name: "level", Value: "info"},
	}
	mockWriter.AssertCalled(t, "WriteEntry", now.Time(), AggregatedMetricEntry(now, 6.0, 3.0, "foo_service", lbls), lbls)

	require.Len(t, c.rawSamples, 0)
}

func TestAggregatedMetricEntry(t *testing.T) {
	ts := time.Now().Truncate(time.Second).UTC()
	totalBytes := uint64(1024)
	totalCount := uint64(5)
	service := "testService"
	emptyLbls := labels.Labels{}
	t.Run("it includes count, bytes, and service name", func(t *testing.T) {
		expected := fmt.Sprintf(
			"ts=%d bytes=%s count=%d %s=%s",
			ts.UnixNano(),
			humanize.Bytes(totalBytes),
			totalCount,
			detection.LabelServiceName, service,
		)

		result := AggregatedMetricEntry(
			model.TimeFromUnix(ts.Unix()),
			totalBytes,
			totalCount,
			service,
			emptyLbls,
		)

		assert.Equal(t, expected, result)
	})

	t.Run("it includes count and bytes for each label key", func(t *testing.T) {
		lbls := labels.Labels{
			labels.Label{Name: "foo", Value: "bar"},
			labels.Label{Name: "test", Value: "test"},
		}

		bytes := humanize.Bytes(totalBytes)
		expected := fmt.Sprintf(
			"ts=%d bytes=%s count=%d %s=%s foo_bytes=%s foo_count=%d test_bytes=%s test_count=%d",
			ts.UnixNano(),
			bytes,
			totalCount,
			detection.LabelServiceName, service,
			bytes, totalCount,
			bytes, totalCount,
		)

		result := AggregatedMetricEntry(
			model.TimeFromUnix(ts.Unix()),
			totalBytes,
			totalCount,
			service,
			lbls,
		)

		assert.Equal(t, expected, result)
	})
}

type mockEntryWriter struct {
	mock.Mock
}

func (m *mockEntryWriter) WriteEntry(ts time.Time, entry string, lbls labels.Labels) {
	_ = m.Called(ts, entry, lbls)
}

func (m *mockEntryWriter) Stop() {
	_ = m.Called()
}
