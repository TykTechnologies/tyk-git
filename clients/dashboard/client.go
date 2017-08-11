package dashboard

import (
	"errors"
	"fmt"
	"github.com/TykTechnologies/tyk/apidef"
	"github.com/levigross/grequests"
	"github.com/ongoingio/urljoin"
)

type Client struct {
	url    string
	secret string
}

type APIResponse struct {
	Message string
	Meta    string
	Status  string
}

type DBApiDefinition struct {
	apidef.APIDefinition `bson:"api_definition,inline" json:"api_definition,inline"`
	HookReferences       []interface{} `bson:"hook_references" json:"hook_references"`
	IsSite               bool          `bson:"is_site" json:"is_site"`
	SortBy               int           `bson:"sort_by" json:"sort_by"`
}

type APISResponse struct {
	Apis  []DBApiDefinition `json:"apis"`
	Pages int               `json:"pages"`
}

const (
	endpointAPIs string = "/api/apis"
)

var (
	UseUpdateError error = errors.New("Object seems to exist (same ID, API ID, Listen Path or Slug), use update()")
	UseCreateError error = errors.New("Object does not exist, use create()")
)

func NewDashboardClient(url, secret string) (*Client, error) {
	return &Client{
		url:    url,
		secret: secret,
	}, nil
}

func (c *Client) fixDBDef(def *DBApiDefinition) {
	if def.HookReferences == nil {
		def.HookReferences = make([]interface{}, 0)
	}
}

func (c *Client) CreateAPI(def *apidef.APIDefinition) (string, error) {
	fullPath := urljoin.Join(c.url, endpointAPIs)

	ro := &grequests.RequestOptions{
		Params: map[string]string{"p": "-2"},
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	}

	resp, err := grequests.Get(fullPath, ro)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API Returned error: %v", resp.String())
	}

	apis := APISResponse{}
	if err := resp.JSON(&apis); err != nil {
		return "", err
	}

	for _, api := range apis.Apis {
		if api.APIID == def.APIID {
			return "", UseUpdateError
		}

		if api.Id == def.Id {
			return "", UseUpdateError
		}

		if api.Slug == def.Slug {
			return "", UseUpdateError
		}

		if api.Proxy.ListenPath == api.Proxy.ListenPath {
			return "", UseUpdateError
		}
	}

	// Create
	asDBDef := DBApiDefinition{APIDefinition: *def}
	c.fixDBDef(&asDBDef)

	createResp, err := grequests.Post(fullPath, &grequests.RequestOptions{
		JSON: asDBDef,
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	})

	if err != nil {
		return "", err
	}

	if createResp.StatusCode != 200 {
		return "", fmt.Errorf("API Returned error: %v (code: %v)", createResp.String(), createResp.StatusCode)
	}

	var status APIResponse
	if err := createResp.JSON(&status); err != nil {
		return "", err
	}

	if status.Status != "OK" {
		return "", fmt.Errorf("API request completed, but with error: %v", status.Message)
	}

	return status.Meta, nil

}

func (c *Client) UpdateAPI(def *apidef.APIDefinition) error {
	fullPath := urljoin.Join(c.url, endpointAPIs)

	ro := &grequests.RequestOptions{
		Params: map[string]string{"p": "-2"},
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	}

	resp, err := grequests.Get(fullPath, ro)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("API Returned error: %v", resp.String())
	}

	apis := APISResponse{}
	if err := resp.JSON(&apis); err != nil {
		return err
	}

	found := false
	for _, api := range apis.Apis {
		// Dashboard uses it's own IDs
		if api.Id == def.Id {
			if def.APIID == "" {
				def.APIID = api.APIID
			}
			found = true
			break
		}
	}

	if !found {
		return UseCreateError
	}

	// Update
	asDBDef := DBApiDefinition{APIDefinition: *def}
	c.fixDBDef(&asDBDef)

	updatePath := urljoin.Join(c.url, endpointAPIs, def.Id.Hex())
	updateResp, err := grequests.Put(updatePath, &grequests.RequestOptions{
		JSON: asDBDef,
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	})

	if err != nil {
		return err
	}

	if updateResp.StatusCode != 200 {
		return fmt.Errorf("API Returned error: %v", updateResp.String())
	}

	var status APIResponse
	if err := updateResp.JSON(&status); err != nil {
		return err
	}

	if status.Status != "OK" {
		return fmt.Errorf("API request completed, but with error: %v", status.Message)
	}

	return nil
}

func (c *Client) Sync(apiDefs []apidef.APIDefinition) error {
	deleteAPIs := []string{}
	updateAPIs := []apidef.APIDefinition{}
	createAPIs := []apidef.APIDefinition{}

	// Fetch the running API list
	fullPath := urljoin.Join(c.url, endpointAPIs)

	ro := &grequests.RequestOptions{
		Params: map[string]string{"p": "-2"},
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	}

	resp, err := grequests.Get(fullPath, ro)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("API Returned error: %v", resp.String())
	}

	apis := APISResponse{}
	if err := resp.JSON(&apis); err != nil {
		return err
	}

	DashIDMap := map[string]int{}
	GitIDMap := map[string]int{}

	// Build the dash ID map
	for i, api := range apis.Apis {
		// Lets get a full list of existing IDs
		DashIDMap[api.Id.Hex()] = i
	}

	// Build the Git ID Map
	for i, def := range apiDefs {
		GitIDMap[def.Id.Hex()] = i
	}

	// Updates are when we find items in git that are also in dash
	for key, index := range GitIDMap {
		_, ok := DashIDMap[key]
		if ok {
			updateAPIs = append(updateAPIs, apiDefs[index])
		}
	}

	// Deletes are when we find items in the dash that are not in git
	for key, _ := range DashIDMap {
		_, ok := GitIDMap[key]
		if !ok {
			deleteAPIs = append(deleteAPIs, key)
		}
	}

	// Create operations are when we find things in Git that are not in the dashboard
	for key, index := range GitIDMap {
		_, ok := DashIDMap[key]
		if !ok {
			createAPIs = append(createAPIs, apiDefs[index])
		}
	}

	// Do the deletes
	for _, dbId := range deleteAPIs {
		fmt.Printf("SYNC Deleting: %v\n", dbId)
		if err := c.deleteAPI(dbId); err != nil {
			return err
		}
	}

	// Do the updates
	for _, api := range updateAPIs {
		fmt.Printf("SYNC Updating: %v\n", api.Id.Hex())
		if err := c.UpdateAPI(&api); err != nil {
			return err
		}
	}

	// Do the creates
	for _, api := range createAPIs {
		fmt.Printf("SYNC Creating: %v\n", api.Name)
		var err error
		var id string
		if id, err = c.CreateAPI(&api); err != nil {
			return err
		}
		fmt.Printf("--> ID: %v\n", id)
	}

	return nil
}

func (c *Client) deleteAPI(id string) error {
	delPath := urljoin.Join(c.url, endpointAPIs, id)
	delResp, err := grequests.Delete(delPath, &grequests.RequestOptions{
		Headers: map[string]string{
			"Authorization": c.secret,
		},
	})

	if err != nil {
		return err
	}

	if delResp.StatusCode != 200 {
		return fmt.Errorf("API Returned error: %v", delResp.String())
	}

	return nil
}