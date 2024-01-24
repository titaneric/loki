package bloomshipper

import (
	"context"
	"fmt"
	"math"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"golang.org/x/exp/slices"

	v1 "github.com/grafana/loki/pkg/storage/bloom/v1"
	"github.com/grafana/loki/pkg/storage/stores/shipper/bloomshipper/config"
)

type fpRange [2]uint64

func (r fpRange) minFp() uint64 {
	return r[0]
}

func (r fpRange) maxFp() uint64 {
	return r[1]
}

type BlockQuerierWithFingerprintRange struct {
	*v1.BlockQuerier
	MinFp, MaxFp model.Fingerprint
}

type ForEachBlockCallback func(bq *v1.BlockQuerier, minFp, maxFp uint64) error

type Interface interface {
	GetBlockRefs(ctx context.Context, tenant string, from, through model.Time) ([]BlockRef, error)
	Fetch(ctx context.Context, tenant string, blocks []BlockRef, callback ForEachBlockCallback) error
	Stop()
}

type Shipper struct {
	client          Client
	config          config.Config
	logger          log.Logger
	blockDownloader *blockDownloader
}

type Limits interface {
	BloomGatewayBlocksDownloadingParallelism(tenantID string) int
}

func NewShipper(client Client, config config.Config, limits Limits, logger log.Logger, reg prometheus.Registerer) (*Shipper, error) {
	logger = log.With(logger, "component", "bloom-shipper")
	downloader, err := newBlockDownloader(config, client, limits, logger, reg)
	if err != nil {
		return nil, fmt.Errorf("error creating block downloader: %w", err)
	}
	return &Shipper{
		client:          client,
		config:          config,
		logger:          logger,
		blockDownloader: downloader,
	}, nil
}

func (s *Shipper) GetBlockRefs(ctx context.Context, tenantID string, from, through model.Time) ([]BlockRef, error) {
	level.Debug(s.logger).Log("msg", "GetBlockRefs", "tenant", tenantID, "from", from, "through", through)

	blockRefs, err := s.getActiveBlockRefs(ctx, tenantID, from, through, []fpRange{{0, math.MaxUint64}})
	if err != nil {
		return nil, fmt.Errorf("error fetching active block references : %w", err)
	}
	return blockRefs, nil
}

func (s *Shipper) Fetch(ctx context.Context, tenantID string, blocks []BlockRef, callback ForEachBlockCallback) error {
	cancelContext, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	resCh, errCh, err := s.blockDownloader.fetch(cancelContext, tenantID, blocks)
	if err != nil {
		return err
	}

	// track how many blocks are still remaning to be downloaded
	remaining := len(blocks)

	for {
		select {
		case <-ctx.Done():
			level.Debug(s.logger).Log("msg", "context is done while downloading block", "err", ctx.Err())
			return fmt.Errorf("failed to fetch blocks: %w", ctx.Err())
		case err := <-errCh:
			level.Debug(s.logger).Log("msg", "received error while downloading block", "err", err)
			return fmt.Errorf("failed to fetch blocks: %w", err)
		case result := <-resCh:
			level.Debug(s.logger).Log("msg", "received downloaded block", "block", result.BlockPath)
			err := runCallback(callback, result, s.logger)
			level.Debug(s.logger).Log("msg", "callback for the block has been completed", "block", result.BlockPath, "err", err)
			if err != nil {
				return err
			}
			remaining--
			level.Debug(s.logger).Log("msg", "remaning ", "remaning", remaining)
			if remaining == 0 {
				level.Debug(s.logger).Log("msg", "remaning is 0 , exiting")
				return nil
			}
		}
	}
}

func runCallback(callback ForEachBlockCallback, block blockWithQuerier, logger log.Logger) error {
	defer func(b blockWithQuerier) {
		level.Debug(logger).Log("msg", "closing block", "block", b.BlockPath)
		_ = b.Close()
		level.Debug(logger).Log("msg", "the block has been closed", "block", b.BlockPath)
	}(block)

	level.Debug(logger).Log("msg", "start running callback", "block", block.BlockPath)
	err := callback(block.closableBlockQuerier.BlockQuerier, block.MinFingerprint, block.MaxFingerprint)
	level.Debug(logger).Log("msg", "end running callback", "block", block.BlockPath)
	if err != nil {
		return fmt.Errorf("error running callback function for block %s err: %w", block.BlockPath, err)
	}
	return nil
}

func (s *Shipper) Stop() {
	s.client.Stop()
	s.blockDownloader.stop()
}

// getFirstLast returns the first and last item of a fingerprint slice
// It assumes an ascending sorted list of fingerprints.
func getFirstLast[T any](s []T) (T, T) {
	var zero T
	if len(s) == 0 {
		return zero, zero
	}
	return s[0], s[len(s)-1]
}

func (s *Shipper) getActiveBlockRefs(ctx context.Context, tenantID string, from, through model.Time, fingerprints []fpRange) ([]BlockRef, error) {
	minFpRange, maxFpRange := getFirstLast(fingerprints)
	metas, err := s.client.GetMetas(ctx, MetaSearchParams{
		TenantID:       tenantID,
		MinFingerprint: model.Fingerprint(minFpRange.minFp()),
		MaxFingerprint: model.Fingerprint(maxFpRange.maxFp()),
		StartTimestamp: from,
		EndTimestamp:   through,
	})
	if err != nil {
		return []BlockRef{}, fmt.Errorf("error fetching meta.json files: %w", err)
	}
	level.Debug(s.logger).Log("msg", "dowloaded metas", "count", len(metas))
	activeBlocks := s.findBlocks(metas, from, through, fingerprints)
	slices.SortStableFunc(activeBlocks, func(a, b BlockRef) int {
		if a.MinFingerprint < b.MinFingerprint {
			return -1
		}
		if a.MinFingerprint > b.MinFingerprint {
			return 1
		}

		return 0
	})
	return activeBlocks, nil
}

func (s *Shipper) findBlocks(metas []Meta, startTimestamp, endTimestamp model.Time, fingerprints []fpRange) []BlockRef {
	outdatedBlocks := make(map[string]interface{})
	for _, meta := range metas {
		for _, tombstone := range meta.Tombstones {
			outdatedBlocks[tombstone.BlockPath] = nil
		}
	}
	blocksSet := make(map[string]BlockRef)
	for _, meta := range metas {
		for _, block := range meta.Blocks {
			if _, contains := outdatedBlocks[block.BlockPath]; contains {
				continue
			}
			if isOutsideRange(&block, startTimestamp, endTimestamp, fingerprints) {
				continue
			}
			blocksSet[block.BlockPath] = block
		}
	}
	blockRefs := make([]BlockRef, 0, len(blocksSet))
	for _, ref := range blocksSet {
		blockRefs = append(blockRefs, ref)
	}
	return blockRefs
}

// isOutsideRange tests if a given BlockRef b is outside of search boundaries
// defined by min/max timestamp and min/max fingerprint.
// Fingerprint ranges must be sorted in ascending order.
func isOutsideRange(b *BlockRef, startTimestamp, endTimestamp model.Time, fingerprints []fpRange) bool {
	// First, check time range
	if b.EndTimestamp < startTimestamp || b.StartTimestamp > endTimestamp {
		return true
	}

	// Then, check if outside of min/max of fingerprint slice
	minFpRange, maxFpRange := getFirstLast(fingerprints)
	if b.MaxFingerprint < minFpRange.minFp() || b.MinFingerprint > maxFpRange.maxFp() {
		return true
	}

	prev := fpRange{0, 0}
	for i := 0; i < len(fingerprints); i++ {
		fpr := fingerprints[i]
		if b.MinFingerprint > prev.maxFp() && b.MaxFingerprint < fpr.minFp() {
			return true
		}
		prev = fpr
	}

	return false
}
