package store

import "testing"

func TestIsLegacyDefaultThumbnailURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "legacy hardcoded size",
			url:  "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=1&high=240&width=240&size=111,240&source=209678993&imageType=origin",
			want: true,
		},
		{
			name: "real landscape size",
			url:  "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=1&high=240&width=240&size=240,180&source=209678993&imageType=origin",
			want: false,
		},
		{
			name: "unrelated image",
			url:  "https://tvax3.sinaimg.cn/crop.0.0.503.503.50/avatar.jpg",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isLegacyDefaultThumbnailURL(test.url); got != test.want {
				t.Fatalf("isLegacyDefaultThumbnailURL() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsUnresolvedLegacyThumbnail(t *testing.T) {
	legacyURL := "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=1&high=240&width=240&size=111,240&source=209678993&imageType=origin"
	if !isUnresolvedLegacyThumbnail(Attachment{AttachmentType: "image", URL: legacyURL, Meta: []byte(`{"fids":["1"]}`)}) {
		t.Fatal("legacy generated thumbnail should wait for a real resolved URL")
	}
	if isUnresolvedLegacyThumbnail(Attachment{AttachmentType: "image", URL: legacyURL, Meta: []byte(`{"resolved_url":"` + legacyURL + `"}`)}) {
		t.Fatal("a resolved thumbnail with a legitimate 111,240 ratio must remain visible")
	}
}

func TestIsDirectImageReference(t *testing.T) {
	imageURL := "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5320938923230262&size=240,240"
	if !isDirectImageReference(imageURL, imageURL) {
		t.Fatal("a DOM image request must be treated as a direct image reference")
	}
	if isDirectImageReference("https://api.weibo.com/webim/groupchat/query_messages.json", imageURL) {
		t.Fatal("an API payload must not be treated as a direct image reference")
	}
}

func TestImageURLMatchesFID(t *testing.T) {
	if !imageURLMatchesFID("https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5322381209113145&size=240,136", "5322381209113145") {
		t.Fatal("matching MSS fid should be accepted")
	}
	if imageURLMatchesFID("https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=999&size=240,136", "5322381209113145") {
		t.Fatal("mismatched MSS fid should be rejected")
	}
	if !imageURLMatchesFID("https://wx1.sinaimg.cn/large/abc123.jpg", "5322381209113145") {
		t.Fatal("a scoped sinaimg URL does not need to embed the numeric fid")
	}
	if imageURLMatchesFID("https://h5.sinaimg.cn/upload/100/1158/2020/05/06/videopicfeed3.png", "5322381209113145") {
		t.Fatal("the fixed group-role crown must never resolve an image fid")
	}
}

func TestNormalizeWeiboThumbnailURL(t *testing.T) {
	got := normalizeWeiboThumbnailURL(
		"https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5321448476379671&high=600&width=600&size=480,600",
		"https://upload.api.weibo.com/2/mss/meta_query.json?source=209678993&fid=5321448476379671&replace=false&callback=__jp11",
	)
	want := "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5321448476379671&high=240&imageType=origin&size=192%2C240&source=209678993&width=240"
	if got != want {
		t.Fatalf("normalizeWeiboThumbnailURL() = %q, want %q", got, want)
	}
}
