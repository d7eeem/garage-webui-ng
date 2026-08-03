package router

import (
	"khairul169/garage-webui/schema"
	"khairul169/garage-webui/utils"
	"net/http"
)

type Config struct{}

func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	utils.ResponseSuccess(w, resp)
}
