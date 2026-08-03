package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func runMediaResolutionWorker(ctx context.Context, st *store.Store, imageURLByFID *imageURLCache, target *historyPageTarget) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		targetURL := target.Get()
		if targetURL != "" {
			job, err := st.ClaimMediaResolution(ctx, 2*time.Minute)
			switch {
			case err == nil:
				jobCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
				_, resolveErr := resolveImageFID(jobCtx, st, imageURLByFID, targetURL, job.FID)
				cancel()
				if resolveErr != nil {
					slog.Warn("media resolution job failed", "jobID", job.ID, "messageID", job.MessageID, "fid", job.FID, "error", resolveErr)
					if failErr := st.FailMediaResolution(ctx, job, resolveErr); failErr != nil {
						slog.Warn("mark media resolution failed", "jobID", job.ID, "error", failErr)
					}
					continue
				}
				if completeErr := st.CompleteMediaResolution(ctx, job.ID); completeErr != nil {
					slog.Warn("complete media resolution job failed", "jobID", job.ID, "error", completeErr)
				}
				continue
			case !store.IsNoMediaResolutionJob(err):
				slog.Warn("claim media resolution job failed", "error", err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func resolveHistoryImageAttachments(ctx context.Context, st *store.Store, imageURLByFID *imageURLCache, targetURL string, attachments []browser.NetworkAttachment) error {
	var resolutionErrors []error
	for _, attachment := range attachments {
		if attachment.AttachmentType != "image" || strings.TrimSpace(attachment.URL) != "" {
			continue
		}
		for _, fid := range networkAttachmentFIDs(attachment) {
			resolveCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			_, err := resolveImageFID(resolveCtx, st, imageURLByFID, targetURL, fid)
			cancel()
			if err != nil {
				resolutionErrors = append(resolutionErrors, err)
			}
		}
	}
	return errors.Join(resolutionErrors...)
}

func resolveImageFID(ctx context.Context, st *store.Store, imageURLByFID *imageURLCache, targetURL string, fid string) (int64, error) {
	fid = strings.TrimSpace(fid)
	if fid == "" {
		return 0, nil
	}
	if imageURL, ok := imageURLByFID.Get(fid); ok {
		count, err := persistResolvedImageFID(ctx, st, fid, imageURL, imageURL)
		if err != nil {
			return 0, err
		}
		slog.Info("image attachment resolved from captured page resource", "fid", fid, "resource", mediaURLLogValue(imageURL), "updated", count)
		return count, nil
	}

	imageURL, sourceURL, err := browser.FetchImageThumbnailURL(ctx, targetURL, fid)
	if err != nil {
		return 0, err
	}
	imageURLByFID.Set(fid, imageURL)
	count, err := persistResolvedImageFID(ctx, st, fid, imageURL, sourceURL)
	if err != nil {
		return 0, err
	}
	slog.Info("image attachment resolved from metadata", "fid", fid, "resource", mediaURLLogValue(imageURL), "updated", count)
	return count, nil
}

func persistResolvedImageFID(ctx context.Context, st *store.Store, fid string, imageURL string, sourceURL string) (int64, error) {
	return st.ResolveImageAttachmentURL(ctx, store.ResolveImageAttachmentParams{
		FID:       fid,
		URL:       imageURL,
		SourceURL: sourceURL,
		Force:     true,
	})
}
