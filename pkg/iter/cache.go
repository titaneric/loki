package iter

import (
	"github.com/apache/arrow/go/v14/arrow"
	"github.com/apache/arrow/go/v14/arrow/array"
	"github.com/grafana/loki/pkg/logproto"
)

type CacheEntryIterator interface {
	EntryIterator
	Wrapped() EntryIterator
	Reset()
}

// cachedIterator is an iterator that caches iteration to be replayed later on.
type cachedIterator struct {
	cache   []entryWithLabels
	wrapped EntryIterator // once set to nil it means we have to use the cache.

	curr int

	closeErr error
	iterErr  error
}

// NewCachedIterator creates an iterator that cache iteration result and can be iterated again
// after closing it without re-using the underlaying iterator `it`.
func NewCachedIterator(it EntryIterator, cap int) CacheEntryIterator {
	c := &cachedIterator{
		wrapped: it,
		cache:   make([]entryWithLabels, 0, cap),
		curr:    -1,
	}
	return c
}

func (it *cachedIterator) Reset() {
	it.curr = -1
}

func (it *cachedIterator) Wrapped() EntryIterator {
	return it.wrapped
}

func (it *cachedIterator) consumeWrapped() bool {
	if it.Wrapped() == nil {
		return false
	}
	ok := it.Wrapped().Next()
	// we're done with the base iterator.
	if !ok {
		it.closeErr = it.Wrapped().Close()
		it.iterErr = it.Wrapped().Error()
		it.wrapped = nil
		return false
	}
	// we're caching entries
	it.cache = append(it.cache, entryWithLabels{Entry: it.Wrapped().Entry(), labels: it.Wrapped().Labels(), streamHash: it.Wrapped().StreamHash()})
	it.curr++
	return true
}

func (it *cachedIterator) Next() bool {
	if len(it.cache) == 0 && it.Wrapped() == nil {
		return false
	}
	if it.curr+1 >= len(it.cache) {
		if it.Wrapped() != nil {
			return it.consumeWrapped()
		}
		return false
	}
	it.curr++
	return true
}

func (it *cachedIterator) Entry() logproto.Entry {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return logproto.Entry{}
	}

	return it.cache[it.curr].Entry
}

func (it *cachedIterator) Labels() string {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return ""
	}
	return it.cache[it.curr].labels
}

func (it *cachedIterator) StreamHash() uint64 {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return 0
	}
	return it.cache[it.curr].streamHash
}

func (it *cachedIterator) Error() error { return it.iterErr }

func (it *cachedIterator) Close() error {
	it.Reset()
	return it.closeErr
}

type CacheSampleIterator interface {
	SampleIterator
	Wrapped() SampleIterator
	Reset()
}

type CacheBatchSampleIterator interface {
	BatchSampleIterator
	Wrapped() BatchSampleIterator
	Reset()
}

// cachedIterator is an iterator that caches iteration to be replayed later on.
type cachedSampleIterator struct {
	cache   []sampleWithLabels
	wrapped SampleIterator

	curr int

	closeErr error
	iterErr  error
}

// NewCachedSampleIterator creates an iterator that cache iteration result and can be iterated again
// after closing it without re-using the underlaying iterator `it`.
func NewCachedSampleIterator(it SampleIterator, cap int) CacheSampleIterator {
	c := &cachedSampleIterator{
		wrapped: it,
		cache:   make([]sampleWithLabels, 0, cap),
		curr:    -1,
	}
	return c
}

func (it *cachedSampleIterator) Wrapped() SampleIterator {
	return it.wrapped
}

func (it *cachedSampleIterator) Reset() {
	it.curr = -1
}

func (it *cachedSampleIterator) consumeWrapped() bool {
	if it.Wrapped() == nil {
		return false
	}
	ok := it.Wrapped().Next()
	// we're done with the base iterator.
	if !ok {
		it.closeErr = it.Wrapped().Close()
		it.iterErr = it.Wrapped().Error()
		it.wrapped = nil
		return false
	}
	// we're caching entries
	it.cache = append(it.cache, sampleWithLabels{Sample: it.Wrapped().Sample(), labels: it.Wrapped().Labels(), streamHash: it.Wrapped().StreamHash()})
	it.curr++
	return true
}

func (it *cachedSampleIterator) Next() bool {
	if len(it.cache) == 0 && it.Wrapped() == nil {
		return false
	}
	if it.curr+1 >= len(it.cache) {
		if it.Wrapped() != nil {
			return it.consumeWrapped()
		}
		return false
	}
	it.curr++
	return true
}

func (it *cachedSampleIterator) Sample() logproto.Sample {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return logproto.Sample{}
	}
	return it.cache[it.curr].Sample
}

func (it *cachedSampleIterator) Labels() string {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return ""
	}
	return it.cache[it.curr].labels
}

func (it *cachedSampleIterator) StreamHash() uint64 {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return 0
	}
	return it.cache[it.curr].streamHash
}

func (it *cachedSampleIterator) Error() error { return it.iterErr }

func (it *cachedSampleIterator) Close() error {
	it.Reset()
	return it.closeErr
}

// cachedIterator is an iterator that caches iteration to be replayed later on.
type cacheBatchSampleIterator struct {
	cache []samplesWithLabels

	wrapped BatchSampleIterator

	curr int

	closeErr error
	iterErr  error
}

// NewCachedSampleIterator creates an iterator that cache iteration result and can be iterated again
// after closing it without re-using the underlaying iterator `it`.
func NewCacheBatchSampleIterator(it BatchSampleIterator, cap int) CacheBatchSampleIterator {
	c := &cacheBatchSampleIterator{
		wrapped: it,
		cache:   make([]samplesWithLabels, 0, cap),
		curr:    -1,
	}
	return c
}

func (it *cacheBatchSampleIterator) Wrapped() BatchSampleIterator {
	return it.wrapped
}

func (it *cacheBatchSampleIterator) Reset() {
	it.curr = -1
}

func (it *cacheBatchSampleIterator) consumeWrapped() bool {
	if it.Wrapped() == nil {
		return false
	}
	ok := it.Wrapped().Next()
	// we're done with the base iterator.
	if !ok {
		it.closeErr = it.Wrapped().Close()
		it.iterErr = it.Wrapped().Error()
		it.wrapped = nil
		return false
	}
	// we're caching entries
	it.cache = append(it.cache, samplesWithLabels{samples: it.Wrapped().Samples(), labels: it.Wrapped().Labels(), streamHash: it.Wrapped().StreamHash()})
	it.curr++
	return true
}

func (it *cacheBatchSampleIterator) Next() bool {
	if len(it.cache) == 0 && it.Wrapped() == nil {
		return false
	}
	if it.curr+1 >= len(it.cache) {
		if it.Wrapped() != nil {
			return it.consumeWrapped()
		}
		return false
	}
	it.curr++
	return true
}

func (it *cacheBatchSampleIterator) Samples() arrow.Record {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return array.NewRecord(nil, nil, 0)
	}
	return it.cache[it.curr].samples
}

func (it *cacheBatchSampleIterator) Labels() []string {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return []string{""}
	}
	return it.cache[it.curr].labels
}

func (it *cacheBatchSampleIterator) StreamHash() uint64 {
	if len(it.cache) == 0 || it.curr < 0 || it.curr >= len(it.cache) {
		return 0
	}
	return it.cache[it.curr].streamHash
}

func (it *cacheBatchSampleIterator) Error() error { return it.iterErr }

func (it *cacheBatchSampleIterator) Close() error {
	it.Reset()
	return it.closeErr
}
