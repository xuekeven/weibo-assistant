package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func sendNotificationImages(ctx context.Context, cfg config.Config, st *store.Store, client *notifier.WeworkClient, message store.Message, job store.NotificationOutboxJob) (notifier.SendResponse, error) {
	imageCount := 0
	var lastResponse notifier.SendResponse
	for _, attachment := range message.Attachments {
		if !strings.EqualFold(strings.TrimSpace(attachment.AttachmentType), "image") {
			continue
		}
		imageCount++
		if imageCount <= job.ImageSentCount {
			continue
		}

		imageCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		image, contentType, err := readNotificationImage(imageCtx, cfg, attachment)
		cancel()
		if err != nil {
			return lastResponse, fmt.Errorf("read image attachment %d for message %d: %w", imageCount, message.ID, err)
		}
		filename := notificationImageFilename(attachment, imageCount, contentType)
		lastResponse, err = client.SendImageBytes(ctx, filename, image)
		if err != nil {
			return lastResponse, fmt.Errorf("send image attachment %d for message %d: %w", imageCount, message.ID, err)
		}
		if err := st.MarkNotificationOutboxImageSent(ctx, job.ID); err != nil {
			return lastResponse, fmt.Errorf("mark image attachment %d for notification %d: %w", imageCount, job.ID, err)
		}
	}
	if imageCount == 0 {
		return lastResponse, fmt.Errorf("image message %d has no image attachments", message.ID)
	}
	if job.ImageSentCount > imageCount {
		return lastResponse, fmt.Errorf("notification %d image progress %d exceeds attachment count %d", job.ID, job.ImageSentCount, imageCount)
	}
	return lastResponse, nil
}

func readNotificationImage(ctx context.Context, cfg config.Config, attachment store.Attachment) ([]byte, string, error) {
	if localPath := strings.TrimSpace(attachment.LocalPath); localPath != "" {
		return readNotificationImageFile(localPath)
	}
	rawURL := strings.TrimSpace(attachment.URL)
	if rawURL == "" {
		return nil, "", fmt.Errorf("image URL is not resolved")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, "", fmt.Errorf("invalid image URL")
	}
	originalURL := browser.OriginalWeiboImageURL(parsed.String())
	image, contentType, err := browser.FetchAuthenticatedImage(ctx, cfg.ChromeDebugPort, originalURL, 0)
	if err != nil {
		return nil, "", err
	}
	if err := validateNotificationImage(image, contentType); err != nil {
		return nil, "", err
	}
	return image, contentType, nil
}

func readNotificationImageFile(path string) ([]byte, string, error) {
	image, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	contentType := http.DetectContentType(image)
	if err := validateNotificationImage(image, contentType); err != nil {
		return nil, "", err
	}
	return image, contentType, nil
}

func validateNotificationImage(image []byte, contentType string) error {
	if len(image) <= 5 {
		return fmt.Errorf("image is too small")
	}
	if contentType != "image/png" && contentType != "image/jpeg" {
		return fmt.Errorf("unsupported image format %q", contentType)
	}
	return nil
}

func notificationImageFilename(attachment store.Attachment, index int, contentType string) string {
	extension := ".jpg"
	if contentType == "image/png" {
		extension = ".png"
	}
	if rawURL := strings.TrimSpace(attachment.URL); rawURL != "" {
		if parsed, err := url.Parse(rawURL); err == nil {
			candidate := strings.ToLower(filepath.Ext(parsed.Path))
			if candidate == ".png" || candidate == ".jpg" || candidate == ".jpeg" {
				extension = candidate
			}
		}
	}
	return fmt.Sprintf("weibo-image-%d%s", index, extension)
}
