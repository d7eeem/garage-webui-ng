package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"khairul169/garage-webui/schema"
	"khairul169/garage-webui/utils"
	"mime"
	"net/http"
	"net/url"
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

		w.Header().Set("Content-Type", "image/png")
		w.Write(thumb)
		return
	}

	w.Header().Set("Cache-Control", "max-age=86400")
	w.Header().Set("Last-Modified", object.LastModified.Format(time.RFC1123))

	if object.ContentType != nil {
		w.Header().Set("Content-Type", *object.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
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

func (b *Browse) PutObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	isDirectory := strings.HasSuffix(key, "/")

	file, headers, err := r.FormFile("file")
	if err != nil && !isDirectory {
		utils.ResponseError(w, err)
		return
	}

	if file != nil {
		defer file.Close()
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	var contentType string = ""
	var size int64 = 0

	if file != nil {
		contentType = headers.Header.Get("Content-Type")
		size = headers.Size
	}

	result, err := client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          file,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})

	if err != nil {
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
				if len(res.Errors) > 0 {
					utils.ResponseError(w, fmt.Errorf("cannot delete object: %v", res.Errors[0]))
					return
				}
				deleted += len(res.Deleted)
			}

			if objects.IsTruncated == nil || !*objects.IsTruncated {
				break
			}
			if objects.NextContinuationToken == nil {
				break
			}
			continuationToken = objects.NextContinuationToken
		}

		utils.ResponseSuccess(w, map[string]int{"deleted": deleted})
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
	creds, err := getBucketCredentials(bucket)
	if err != nil {
		return nil, fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)
	}

	// Determine endpoint and whether to disable HTTPS
	endpoint := utils.Garage.GetS3Endpoint()
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
