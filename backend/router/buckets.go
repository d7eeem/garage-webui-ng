package router

import (
	"encoding/json"
	"fmt"
	"khairul169/garage-webui/schema"
	"khairul169/garage-webui/utils"
	"net/http"
	"sync"
)

type Buckets struct{}

// maxBucketInfoConcurrency bounds how many GetBucketInfo calls run at once.
// Without a bound, a cluster with thousands of buckets fires one admin API
// request per bucket simultaneously.
const maxBucketInfoConcurrency = 8

func (b *Buckets) GetAll(w http.ResponseWriter, r *http.Request) {
	body, err := utils.Garage.Fetch("/v2/ListBuckets", &utils.FetchOptions{})
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	var buckets []schema.GetBucketsRes
	if err := json.Unmarshal(body, &buckets); err != nil {
		utils.ResponseError(w, err)
		return
	}

	res := make([]schema.Bucket, len(buckets))
	sem := make(chan struct{}, maxBucketInfoConcurrency)
	var wg sync.WaitGroup

	for i, bucket := range buckets {
		wg.Add(1)
		sem <- struct{}{}

		go func(i int, bucket schema.GetBucketsRes) {
			defer wg.Done()
			defer func() { <-sem }()

			fallback := schema.Bucket{ID: bucket.ID, GlobalAliases: bucket.GlobalAliases}

			body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetBucketInfo?id=%s", bucket.ID), &utils.FetchOptions{})
			if err != nil {
				res[i] = fallback
				return
			}

			var data schema.Bucket
			if err := json.Unmarshal(body, &data); err != nil {
				res[i] = fallback
				return
			}

			data.LocalAliases = bucket.LocalAliases
			res[i] = data
		}(i, bucket)
	}

	wg.Wait()
	utils.ResponseSuccess(w, res)
}
