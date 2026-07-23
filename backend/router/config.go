package router

import (
	"khairul169/garage-webui/schema"
	"khairul169/garage-webui/utils"
	"net/http"
)

type Config struct{}

func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	utils.ResponseSuccess(w, schema.NewConfigResponse(utils.Garage.Config))
}
