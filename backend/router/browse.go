package router

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/d7eeem/garage-webui-ng/schema"
	"github.com/d7eeem/garage-webui-ng/utils"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type Browse struct{}

func (b *Browse) GetObjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	bucket := r.PathValue("bucket")
	prefix := query.Get("prefix")

	limit := normalizeListLimit(query.Get("limit"))

	var continuationToken *string
	if next := query.Get("next"); next != "" {
		continuationToken = aws.String(next)
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	objects, err := client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{
		Bucket:            aws.String(bucket),
		Prefix:            aws.String(prefix),
		Delimiter:         aws.String("/"),
		MaxKeys:           aws.Int32(limit),
		ContinuationToken: continuationToken,
	})

	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	result := schema.BrowseObjectResult{
		Prefixes:  []string{},
		Objects:   []schema.BrowserObject{},
		Prefix:    prefix,
		NextToken: objects.NextContinuationToken,
	}

	for _, prefix := range objects.CommonPrefixes {
		result.Prefixes = append(result.Prefixes, *prefix.Prefix)
	}

	for _, object := range objects.Contents {
		key := strings.TrimPrefix(*object.Key, prefix)
		if key == "" {
			continue
		}

		result.Objects = append(result.Objects, schema.BrowserObject{
			ObjectKey:    &key,
			LastModified: object.LastModified,
			Size:         object.Size,
			Url:          browseObjectURL(bucket, *object.Key),
		})
	}

	utils.ResponseSuccess(w, result)
}

func (b *Browse) GetOneObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	queryParams := r.URL.Query()
	view := queryParams.Get("view") == "1"
	thumbnail := queryParams.Get("thumb") == "1"
	download := queryParams.Get("dl") == "1"

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	if !view && !download && !thumbnail {
		object, err := client.HeadObject(r.Context(), &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			if isNotFoundErr(err) {
				utils.ResponseErrorStatus(w, err, http.StatusNotFound)
				return
			}
			utils.ResponseError(w, err)
			return
		}
		utils.ResponseSuccess(w, object)
		return
	}

	object, err := client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) && ae.ErrorCode() == "NoSuchKey" {
			utils.ResponseErrorStatus(w, err, http.StatusNotFound)
			return
		}

		utils.ResponseError(w, err)
		return
	}

	defer object.Body.Close()
	keys := strings.Split(key, "/")

	if download {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))
	} else if thumbnail {
		body, err := io.ReadAll(object.Body)
		if err != nil {
			utils.ResponseError(w, err)
			return
		}

		thumb, err := utils.CreateThumbnailImage(body, 64, 64)
		if err != nil {

			utils.ResponseError(w, err)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumb)
		return
	}

	w.Header().Set("Cache-Control", "max-age=86400")
	w.Header().Set("Last-Modified", object.LastModified.Format(time.RFC1123))

	stored := ""
	if object.ContentType != nil {
		stored = *object.ContentType
	}

	// Always: never let the browser second-guess the type we send.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if isInlineSafe(stored) {
		w.Header().Set("Content-Type", stored)
	} else {
		// Unknown or scriptable: hand it to the user as a file rather than
		// rendering it on this origin.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))
	}

	// This body is rendered inside the console's own media viewer (an
	// <iframe> for PDFs), so it must be framable by us. The global
	// X-Frame-Options: DENY from middleware.SecurityHeaders is correct for the
	// console's own HTML but forbids framing by *anyone*, same-origin
	// included — which is what left PDF previews blank. Narrow it to
	// SAMEORIGIN here, and express the same rule for modern browsers with
	// frame-ancestors 'self'. Another site still cannot frame this.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	// Defence in depth: even a type on the allowlist is served with an empty
	// origin and no script execution, so a mislabelled body cannot act.
	w.Header().Set("Content-Security-Policy", objectViewCSP(stored))

	if object.ContentLength != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(*object.ContentLength, 10))
	}
	if object.ETag != nil {
		w.Header().Set("Etag", *object.ETag)
	}

	_, err = io.Copy(w, object.Body)

	if err != nil {
		utils.ResponseError(w, err)
		return
	}
}

// resolveUploadContentType recovers a meaningful Content-Type from the
// object key's extension when the browser's multipart part carried none —
// either empty or the generic "application/octet-stream" default, which is
// what a browser sends when File.type is empty. File.type is empty for any
// extension the OS's local mime database does not know, a common gap for
// .svg, .webp, .avif and .ico (frontend's `mime/lite` has the same gap for
// .ico, which is why this is resolved server-side with the Go stdlib's
// mime.TypeByExtension instead). A non-empty, non-generic contentType from
// the browser is preserved unchanged.
func resolveUploadContentType(contentType, key string) string {
	if contentType == "" || contentType == "application/octet-stream" {
		if guessed := mime.TypeByExtension(path.Ext(key)); guessed != "" {
			return guessed
		}
	}
	return contentType
}

func (b *Browse) PutObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	isDirectory := strings.HasSuffix(key, "/")

	// Reject before ParseMultipartForm buffers anything. Answering while the
	// browser is still streaming is the only way it can read the status at all
	// — once the handler returns without consuming the body, the server closes
	// the connection and the browser reports an opaque network error instead.
	limit := maxUploadBytes()
	if r.ContentLength > limit {
		utils.ResponseErrorStatus(w, fmt.Errorf(
			"upload is too large: %d bytes exceeds the %d MB limit (raise MAX_UPLOAD_SIZE_MB, and any body-size limit on your reverse proxy)",
			r.ContentLength, limit>>20,
		), http.StatusRequestEntityTooLarge)
		return
	}

	// Belt to the Content-Length suspenders: a client that lies about or omits
	// its length is still cut off at the ceiling, and MaxBytesReader makes the
	// overflow surface as a read error from FormFile rather than as unbounded
	// buffering.
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	// Parse explicitly with a small memory budget. r.FormFile would otherwise
	// call ParseMultipartForm(32 MiB), holding up to 32 MiB per concurrent
	// upload in RAM; 4 MiB is enough for the form's non-file fields and pushes
	// the payload to the temp directory, which the runtime image has.
	if err := r.ParseMultipartForm(4 << 20); err != nil && !isDirectory {
		drainRequestBody(r)
		utils.ResponseError(w, fmt.Errorf("cannot read upload: %w", err))
		return
	}

	file, headers, err := r.FormFile("file")
	if err != nil && !isDirectory {
		drainRequestBody(r)
		utils.ResponseError(w, fmt.Errorf("cannot read uploaded file: %w", err))
		return
	}

	if file != nil {
		defer file.Close()
	}

	client, err := getS3Client(bucket)
	if err != nil {
		drainRequestBody(r)
		utils.ResponseError(w, err)
		return
	}

	var contentType string = ""
	var size int64 = 0

	if file != nil {
		contentType = headers.Header.Get("Content-Type")
		size = headers.Size
	}

	contentType = resolveUploadContentType(contentType, key)

	result, err := client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          file,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})

	if err != nil {
		drainRequestBody(r)
		utils.ResponseError(w, fmt.Errorf("cannot put object: %w", err))
		return
	}

	utils.ResponseSuccess(w, result)
}

func (b *Browse) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	recursive := r.URL.Query().Get("recursive") == "true"
	isDirectory := strings.HasSuffix(key, "/")

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	// Delete directory and its content
	if isDirectory && recursive {
		var deleted int
		// Initialized non-nil so the JSON response always carries "errors":[]
		// rather than "errors":null when nothing failed — a nil slice
		// marshals to null, and the frontend calls .map/.length on this
		// field unconditionally.
		failed := []map[string]string{}
		var continuationToken *string

		for {
			objects, err := client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				Prefix:            aws.String(key),
				ContinuationToken: continuationToken,
			})
			if err != nil {
				utils.ResponseError(w, err)
				return
			}

			keys := make([]types.ObjectIdentifier, 0, len(objects.Contents))
			for _, object := range objects.Contents {
				keys = append(keys, types.ObjectIdentifier{Key: object.Key})
			}

			for _, batch := range chunkObjectIdentifiers(keys, maxListKeys) {
				res, err := client.DeleteObjects(r.Context(), &s3.DeleteObjectsInput{
					Bucket: aws.String(bucket),
					Delete: &types.Delete{Objects: batch},
				})
				if err != nil {
					utils.ResponseError(w, fmt.Errorf("cannot delete object: %w", err))
					return
				}
				deleted += len(res.Deleted)
				failed = append(failed, deleteErrorsToList(res.Errors)...)
			}

			if objects.IsTruncated == nil || !*objects.IsTruncated {
				break
			}
			if objects.NextContinuationToken == nil {
				break
			}
			continuationToken = objects.NextContinuationToken
		}

		utils.ResponseSuccess(w, map[string]any{"deleted": deleted, "errors": failed})
		return
	}

	// Delete single object
	res, err := client.DeleteObject(r.Context(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot delete object: %w", err))
		return
	}

	utils.ResponseSuccess(w, res)
}

// POST /browse/{bucket}  body: {"action":"delete","keys":["a","b/c",...]}
func (b *Browse) BulkDeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var body struct {
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}
	if body.Action != "delete" {
		utils.ResponseErrorStatus(w, fmt.Errorf("unsupported action %q", body.Action), http.StatusBadRequest)
		return
	}
	if len(body.Keys) == 0 {
		utils.ResponseSuccess(w, map[string]any{"deleted": 0, "errors": []any{}})
		return
	}
	if len(body.Keys) > maxListKeys {
		utils.ResponseErrorStatus(w, fmt.Errorf("too many keys: %d (max %d)", len(body.Keys), maxListKeys), http.StatusBadRequest)
		return
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	ids := make([]types.ObjectIdentifier, 0, len(body.Keys))
	for _, k := range body.Keys {
		ids = append(ids, types.ObjectIdentifier{Key: aws.String(k)})
	}

	deleted := 0
	// Initialized non-nil so the JSON response always carries "errors":[]
	// rather than "errors":null when nothing failed — a nil slice marshals
	// to null, and the frontend calls .map/.length on this field
	// unconditionally.
	failed := []map[string]string{}
	for _, batch := range chunkObjectIdentifiers(ids, maxListKeys) {
		res, err := client.DeleteObjects(r.Context(), &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: batch},
		})
		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot delete objects: %w", err))
			return
		}
		deleted += len(res.Deleted)
		failed = append(failed, deleteErrorsToList(res.Errors)...)
	}
	utils.ResponseSuccess(w, map[string]any{"deleted": deleted, "errors": failed})
}

// downloadToken is what utils.Cache stores under downloadTokenCacheKey(token).
// It is a bearer credential — short-lived, single-use, and bound to both the
// bucket and the session that minted it — so a leaked archive URL cannot be
// replayed or reused for a different bucket/user. Loosening any of those
// three properties makes the archive URL shareable.
type downloadToken struct {
	Bucket   string
	Keys     []string
	Username string
}

// downloadTokenTTL is how long a minted token remains valid before the
// browser must have started the GET that streams the archive.
const downloadTokenTTL = 60 * time.Second

func downloadTokenCacheKey(token string) string {
	return "dlzip:" + token
}

// POST /browse/download-token  body: {"bucket":"...","keys":["a","b/c",...]}
//
// Mints a short-lived, single-use, bucket+user-bound token that authorises
// GET /browse/{bucket}/archive. This is deliberately a SEPARATE route from
// POST /browse/{bucket} (which serves BulkDeleteObjects's delete action):
// folding this into that route would force the viewer carve-out in
// isViewerAllowed to also allow delete. Never merge them.
//
// A native browser download cannot send the X-CSRF-Token header that a
// mutating request normally requires, and the selected key list is too large
// to fit in a URL — so minting happens over a normal POST (via the JS `api`
// client, which does attach the CSRF header), and the archive itself is
// fetched with a plain GET navigation carrying only this token.
func (b *Browse) CreateDownloadToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bucket string   `json:"bucket"`
		Keys   []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}
	if body.Bucket == "" {
		utils.ResponseErrorStatus(w, fmt.Errorf("bucket is required"), http.StatusBadRequest)
		return
	}
	if len(body.Keys) == 0 {
		utils.ResponseErrorStatus(w, fmt.Errorf("keys are required"), http.StatusBadRequest)
		return
	}
	if len(body.Keys) > maxListKeys {
		utils.ResponseErrorStatus(w, fmt.Errorf("too many keys: %d (max %d)", len(body.Keys), maxListKeys), http.StatusBadRequest)
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot generate download token: %w", err))
		return
	}
	token := hex.EncodeToString(tokenBytes)

	username, _ := utils.Session.Get(r, "username").(string)

	utils.Cache.Set(downloadTokenCacheKey(token), downloadToken{
		Bucket:   body.Bucket,
		Keys:     body.Keys,
		Username: username,
	}, downloadTokenTTL)

	utils.ResponseSuccess(w, map[string]any{"token": token})
}

// GET /browse/{bucket}/archive?token=<token> — streams the objects named by a
// token minted via CreateDownloadToken as a single ZIP.
//
// The HTTP status is committed the moment the first byte is written below, so
// everything that can still fail with a clean HTTP error status (missing/
// invalid/expired/mismatched token, cannot reach S3) MUST be checked before
// zip.NewWriter starts writing. After that point, a failure on an individual
// object can only be reported inside the archive itself
// (DOWNLOAD-ERRORS.txt) — there is no way to turn a 200 already sent into an
// error status.
func (b *Browse) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	token := r.URL.Query().Get("token")
	if token == "" {
		utils.ResponseErrorStatus(w, fmt.Errorf("token is required"), http.StatusBadRequest)
		return
	}

	cacheKey := downloadTokenCacheKey(token)
	cached := utils.Cache.Get(cacheKey)
	if cached == nil {
		utils.ResponseErrorStatus(w, fmt.Errorf("download link expired"), http.StatusNotFound)
		return
	}
	dl := cached.(downloadToken)

	// Single-use: invalidate immediately after a successful lookup (by
	// overwriting the entry with an already-expired TTL) so a leaked or
	// replayed URL cannot be used twice, regardless of what the
	// bucket/username check below decides.
	utils.Cache.Set(cacheKey, dl, -time.Second)

	username, _ := utils.Session.Get(r, "username").(string)
	if dl.Bucket != bucket || dl.Username != username {
		utils.ResponseErrorStatus(w, fmt.Errorf("token is not valid for this bucket/user"), http.StatusForbidden)
		return
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(bucket+"-objects.zip"))
	w.Header().Set("Cache-Control", "no-store")

	entryNames := stripCommonKeyPrefix(dl.Keys)

	// From here on the status is committed — see the function comment.
	zw := zip.NewWriter(w)

	var failures []string
	for _, key := range dl.Keys {
		if err := r.Context().Err(); err != nil {
			// The client disconnected or the request was cancelled: stop
			// doing work immediately rather than fetching more objects
			// nobody will receive.
			break
		}

		obj, err := client.GetObject(r.Context(), &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			log.Printf("download archive: cannot get object %q from bucket %q: %v", key, bucket, err)
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
			continue
		}

		entryWriter, err := zw.Create(entryNames[key])
		if err != nil {
			obj.Body.Close()
			log.Printf("download archive: cannot create zip entry for %q: %v", key, err)
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
			continue
		}

		_, err = io.Copy(entryWriter, obj.Body)
		obj.Body.Close()
		if err != nil {
			log.Printf("download archive: cannot copy object %q into archive: %v", key, err)
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
		}
	}

	if len(failures) > 0 {
		if entryWriter, err := zw.Create("DOWNLOAD-ERRORS.txt"); err == nil {
			body := "The following objects could not be added to this archive:\n\n" +
				strings.Join(failures, "\n") + "\n"
			io.WriteString(entryWriter, body)
		}
	}

	// zw.Close's error is best-effort: the client may have already
	// disconnected, and the HTTP status is already committed either way.
	_ = zw.Close()
}

// stripCommonKeyPrefix computes the archive entry name for each key by
// removing the longest common directory prefix shared by all of them, so
// selecting several files that live under the same folder (e.g.
// "assets/css/main.css", "assets/css/vars.css") doesn't repeat that folder
// in every archive entry name ("main.css", "vars.css" instead). Collision-
// free by construction: stripping only ever removes a prefix all keys share,
// so any two keys that still differ after their common directory do so on
// the remaining, distinct portion of the key.
func stripCommonKeyPrefix(keys []string) map[string]string {
	entries := make(map[string]string, len(keys))
	prefix := commonDirPrefix(keys)
	for _, k := range keys {
		name := strings.TrimPrefix(k, prefix)
		if name == "" {
			name = path.Base(k)
		}
		entries[k] = name
	}
	return entries
}

// commonDirPrefix returns the longest prefix shared by every key, trimmed
// back to the last '/' so it never cuts a filename in half (e.g. "report1"
// and "report2" share "report", but that is not a directory).
func commonDirPrefix(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	prefix := keys[0]
	for _, k := range keys[1:] {
		prefix = commonStringPrefix(prefix, k)
		if prefix == "" {
			break
		}
	}
	if idx := strings.LastIndex(prefix, "/"); idx >= 0 {
		return prefix[:idx+1]
	}
	return ""
}

// commonStringPrefix returns the longest common byte-wise prefix of a and b.
func commonStringPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// GET /multipart/{bucket} — list unfinished multipart uploads for a bucket.
func (b *Browse) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	out, err := client.ListMultipartUploads(r.Context(), &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot list multipart uploads: %w", err))
		return
	}
	type upload struct {
		Key       string     `json:"key"`
		UploadID  string     `json:"uploadId"`
		Initiated *time.Time `json:"initiated"`
	}
	uploads := make([]upload, 0, len(out.Uploads))
	for _, u := range out.Uploads {
		uploads = append(uploads, upload{
			Key:       aws.ToString(u.Key),
			UploadID:  aws.ToString(u.UploadId),
			Initiated: u.Initiated,
		})
	}
	utils.ResponseSuccess(w, map[string]any{"uploads": uploads})
}

// DELETE /multipart/{bucket}?key=<key>&uploadId=<id>  — abort one
// DELETE /multipart/{bucket}?all=true                 — abort all
func (b *Browse) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	q := r.URL.Query()
	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	abort := func(key, uploadID string) error {
		_, err := client.AbortMultipartUpload(r.Context(), &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		return err
	}

	if q.Get("all") == "true" {
		out, err := client.ListMultipartUploads(r.Context(), &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot list multipart uploads: %w", err))
			return
		}
		aborted := 0
		for _, u := range out.Uploads {
			if err := abort(aws.ToString(u.Key), aws.ToString(u.UploadId)); err != nil {
				utils.ResponseError(w, fmt.Errorf("cannot abort upload: %w", err))
				return
			}
			aborted++
		}
		utils.ResponseSuccess(w, map[string]int{"aborted": aborted})
		return
	}

	key, uploadID := q.Get("key"), q.Get("uploadId")
	if key == "" || uploadID == "" {
		utils.ResponseErrorStatus(w, fmt.Errorf("key and uploadId are required (or all=true)"), http.StatusBadRequest)
		return
	}
	if err := abort(key, uploadID); err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot abort upload: %w", err))
		return
	}
	utils.ResponseSuccess(w, map[string]int{"aborted": 1})
}

// GET /share/{bucket}/{key...}?expires=<seconds> — presigned GET link.
func (b *Browse) ShareObject(w http.ResponseWriter, r *http.Request) {
	if !utils.Garage.IsSharingEnabled() {
		utils.ResponseErrorStatus(w, fmt.Errorf("sharing is not enabled (set S3_PUBLIC_ENDPOINT_URL)"), http.StatusNotImplemented)
		return
	}
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	const def, max = 3600, 604800 // 1h default, 7d cap (SigV4 ceiling)
	expires := def
	if v, err := strconv.Atoi(r.URL.Query().Get("expires")); err == nil && v > 0 {
		expires = v
	}
	if expires > max {
		expires = max
	}

	client, err := getS3ClientForEndpoint(bucket, utils.Garage.GetS3PublicEndpoint())
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	presign := s3.NewPresignClient(client)
	req, err := presign.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(time.Duration(expires)*time.Second))
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot presign object: %w", err))
		return
	}
	utils.ResponseSuccess(w, map[string]any{
		"url":            req.URL,
		"expiresSeconds": expires,
	})
}

// maxListKeys is the S3 per-request cap for both ListObjectsV2 results and
// DeleteObjects inputs. Garage follows the S3 API here.
const maxListKeys = 1000

// normalizeListLimit clamps a caller-supplied page size into the range the S3
// API accepts. Invalid, absent, zero, or negative values fall back to 100;
// anything above the S3 cap is clamped to it.
func normalizeListLimit(raw string) int32 {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > maxListKeys {
		return maxListKeys
	}
	return int32(limit)
}

// isNotFoundErr reports whether an S3 error means "this object does not exist".
//
// The two codes are NOT interchangeable. GetObject returns NoSuchKey with an
// XML error document; HEAD has no response body, so aws-sdk-go-v2 synthesizes
// NotFound instead. A caller that matches only one of them silently misses the
// other — which is why the HEAD branch used to answer 500 for a missing object.
func isNotFoundErr(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "NotFound", "NoSuchKey":
		return true
	}
	return false
}

// defaultMaxUploadBytes caps a single browser upload. The whole body is
// buffered by ParseMultipartForm before any of it reaches Garage, so this is a
// real memory/disk commitment per concurrent upload, not a policy knob. 512 MiB
// is generous for a browser form post; anything larger belongs in a multipart
// S3 upload (deferred — D2b).
const defaultMaxUploadBytes int64 = 512 << 20

// maxUploadBytes returns the configured single-upload ceiling in bytes.
//
// MAX_UPLOAD_SIZE_MB is read as whole megabytes because that is the unit an
// operator matches against their reverse proxy (nginx client_max_body_size,
// Caddy request_body max_size). A missing, unparseable, zero or negative value
// falls back to the default rather than disabling the limit: an accidental
// typo must not turn the cap off.
func maxUploadBytes() int64 {
	raw := utils.GetEnv("MAX_UPLOAD_SIZE_MB", "")
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 {
		return defaultMaxUploadBytes
	}
	return mb << 20
}

// drainRequestBody consumes and discards whatever is left of the request body.
//
// Go's HTTP server only auto-drains a small unread remainder before deciding to
// close the connection. When a handler answers a large upload with an error and
// returns, the socket is reset while the browser is still writing, and the
// browser surfaces "NetworkError"/"Failed to fetch" instead of the status and
// message the handler actually sent. Draining first lets the response through.
// The read is already bounded by the MaxBytesReader installed in PutObject.
func drainRequestBody(r *http.Request) {
	if r.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
}

// chunkObjectIdentifiers splits keys into batches no larger than the
// DeleteObjects per-request cap.
func chunkObjectIdentifiers(keys []types.ObjectIdentifier, size int) [][]types.ObjectIdentifier {
	if size <= 0 {
		size = maxListKeys
	}
	var batches [][]types.ObjectIdentifier
	for start := 0; start < len(keys); start += size {
		end := start + size
		if end > len(keys) {
			end = len(keys)
		}
		batches = append(batches, keys[start:end])
	}
	return batches
}

// deleteErrorsToList converts S3 per-object delete errors into a flat,
// JSON-friendly slice. Reports ALL failures, not just the first.
func deleteErrorsToList(errs []types.Error) []map[string]string {
	out := make([]map[string]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, map[string]string{
			"key":     aws.ToString(e.Key),
			"message": aws.ToString(e.Message),
		})
	}
	return out
}

// browseObjectURL builds the API path for an object, percent-encoding both the
// bucket and the key so that keys containing '?', '#', '%', '+', or spaces
// survive the round trip. Each path segment is escaped individually; the '/'
// separators between segments stay literal so the {key...} wildcard still
// matches them.
func browseObjectURL(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "/browse/" + url.PathEscape(bucket) + "/" + strings.Join(segments, "/")
}

// contentDispositionAttachment builds a Content-Disposition header value that
// survives filenames containing spaces, quotes, semicolons, or non-ASCII
// characters.
func contentDispositionAttachment(filename string) string {
	if disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": filename,
	}); disposition != "" {
		return disposition
	}
	// FormatMediaType rejects values that are not valid UTF-8. Fall back to a
	// percent-encoded RFC 5987 parameter.
	return "attachment; filename*=UTF-8''" + url.PathEscape(filename)
}

// inlineSafeContentTypes are the only stored content types we will let a
// browser render in place on this application's own origin.
//
// Everything else is served as an attachment, because an object body is
// caller-controlled data: anyone with S3 write access to a bucket chooses its
// content type, and an HTML-ish type rendered here would execute script inside
// the console's origin — able to drive authenticated API calls and read the
// deliberately non-HttpOnly csrf_token cookie. Note SVG is NOT here: it is an
// XML document that can carry <script>.
var inlineSafeContentTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/gif":                true,
	"image/webp":               true,
	"image/avif":               true,
	"image/bmp":                true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
	"text/plain":               true,
	"application/pdf":          true,
	"video/mp4":                true,
	"video/webm":               true,
	"audio/mpeg":               true,
	"audio/ogg":                true,
	"audio/wav":                true,
}

// isInlineSafe reports whether a stored content type may be rendered inline.
// Parameters (charset, boundary) are stripped and case normalised, so
// "TEXT/PLAIN; charset=utf-8" matches. A parse error returns false — fail closed.
func isInlineSafe(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return inlineSafeContentTypes[strings.ToLower(mediaType)]
}

// isPDF reports whether a stored content type is application/pdf, parsed the
// same way isInlineSafe parses it (mime.ParseMediaType, lowercased, ignoring
// parameters) so "application/pdf; charset=binary" still matches. A parse
// error returns false — fail closed, matching isInlineSafe.
func isPDF(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.ToLower(mediaType) == "application/pdf"
}

// objectViewCSP builds the Content-Security-Policy for an inline object body.
//
// `sandbox` gives the body an opaque origin with no script execution, so a
// mislabelled body cannot act on the console's origin — that is the property
// worth protecting and it is preserved in every branch below.
//
// PDFs additionally need `allow-scripts`: browsers render PDFs with a viewer
// that is itself scripted, and a fully sandboxed frame blanks it. This is a
// deliberate, narrow relaxation — `allow-same-origin` is NOT granted, so the
// document keeps its opaque origin and still cannot read the console's cookies
// (including the deliberately non-HttpOnly csrf_token), touch the parent DOM,
// or make credentialed same-origin requests. Scripts confined to an opaque
// origin cannot reach anything that matters.
//
// frame-ancestors 'self' lets the console's own viewer frame the body while
// still refusing every other site.
func objectViewCSP(contentType string) string {
	if isPDF(contentType) {
		return "sandbox allow-scripts; frame-ancestors 'self'"
	}
	return "sandbox; frame-ancestors 'self'"
}

func getBucketCredentials(bucket string) (aws.CredentialsProvider, error) {
	cacheKey := fmt.Sprintf("key:%s", bucket)
	cacheData := utils.Cache.Get(cacheKey)

	if cacheData != nil {
		return cacheData.(aws.CredentialsProvider), nil
	}

	body, err := utils.Garage.Fetch("/v2/GetBucketInfo?globalAlias="+bucket, &utils.FetchOptions{})
	if err != nil {
		return nil, err
	}

	var bucketData schema.Bucket
	if err := json.Unmarshal(body, &bucketData); err != nil {
		return nil, err
	}

	var key schema.KeyElement
	var found bool

	for _, k := range bucketData.Keys {
		if !k.Permissions.Read || !k.Permissions.Write {
			continue
		}

		body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetKeyInfo?id=%s&showSecretKey=true", k.AccessKeyID), &utils.FetchOptions{})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &key); err != nil {
			return nil, err
		}
		found = true
		break
	}

	if !found || key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return nil, fmt.Errorf(
			"no access key with read and write permission is assigned to bucket %q; "+
				"grant a key read+write access to this bucket in the Permissions tab",
			bucket,
		)
	}

	credential := credentials.NewStaticCredentialsProvider(key.AccessKeyID, key.SecretAccessKey, "")
	utils.Cache.Set(cacheKey, credential, time.Hour)

	return credential, nil
}

func getS3Client(bucket string) (*s3.Client, error) {
	return getS3ClientForEndpoint(bucket, utils.Garage.GetS3Endpoint())
}

func getS3ClientForEndpoint(bucket, endpoint string) (*s3.Client, error) {
	creds, err := getBucketCredentials(bucket)
	if err != nil {
		return nil, fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)
	}

	// Determine whether to disable HTTPS
	disableHTTPS := !strings.HasPrefix(endpoint, "https://")

	// AWS config without BaseEndpoint
	awsConfig := aws.Config{
		Credentials: creds,
		Region:      utils.Garage.GetS3Region(),
	}

	// Build S3 client with custom endpoint resolver for proper signing
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = true
		o.EndpointOptions.DisableHTTPS = disableHTTPS
		o.EndpointResolver = s3.EndpointResolverFunc(func(region string, opts s3.EndpointResolverOptions) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: utils.Garage.GetS3Region(),
			}, nil
		})
	})

	return client, nil
}
