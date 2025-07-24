```go
package stalkerlib

import (
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

/* StalkerClient represents a client for interacting with Stalker Middleware APIs.
   It handles authentication, channel data, EPG, and logo retrieval with robust error handling and server variability. */
type StalkerClient struct {
	PortalURL string // Stalker portal base URL (e.g., http://example.com)
	MAC       string // MAC address for authentication
	Timezone  string // Timezone for EPG (e.g., UTC, America/New_York)
	Token     string // Authentication token
	Config    ServerConfig // Server-specific capabilities
}

/* ServerConfig holds server-specific capabilities determined by probing. */
type ServerConfig struct {
	SupportsGzip      bool // Whether the server supports gzip-compressed responses
	RequiresCreateLink bool // Whether the server requires create_link for playback URLs
}

/* HandshakeResponse represents the JSON response from the handshake action. */
type HandshakeResponse struct {
	Js struct {
		Token string `json:"token"`
	} `json:"js"`
}

/* Channel represents a single channel from the Stalker API. */
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
	Logo string `json:"logo"`
}

/* ChannelListResponse represents the JSON response from get_all_channels action. */
type ChannelListResponse struct {
	Js struct {
		Channels []Channel `json:"channels"`
	} `json:"js"`
}

/* CreateLinkResponse represents the JSON response from create_link action. */
type CreateLinkResponse struct {
	Js struct {
		Cmd string `json:"cmd"`
	} `json:"js"`
}

/* EPGProgram represents a single EPG program entry. */
type EPGProgram struct {
	ChannelID string `json:"ch_id"`
	Name      string `json:"name"`
	Start     int64  `json:"start_timestamp"`
	Stop      int64  `json:"stop_timestamp"`
	Desc      string `json:"descr"`
	Category  string `json:"category"`
}

/* EPGResponse represents the JSON response from get_epg action. */
type EPGResponse struct {
	Js struct {
		Programs []EPGProgram `json:"programs"`
	} `json:"js"`
}

/* XMLTV represents the structure for XMLTV output. */
type XMLTV struct {
	XMLName  xml.Name `xml:"tv"`
	Channels []XMLTVChannel `xml:"channel"`
	Programs []XMLTVProgram `xml:"programme"`
}

/* XMLTVChannel represents a channel in XMLTV format. */
type XMLTVChannel struct {
	ID          string `xml:"id,attr"`
	DisplayName string `xml:"display-name"`
}

/* XMLTVProgram represents a program in XMLTV format. */
type XMLTVProgram struct {
	Start    string `xml:"start,attr"`
	Stop     string `xml:"stop,attr"`
	Channel  string `xml:"channel,attr"`
	Title    string `xml:"title"`
	Desc     string `xml:"desc"`
	Category string `xml:"category"`
}

/* NewStalkerClient creates a new StalkerClient with the given portal URL, MAC address, and timezone. */
func NewStalkerClient(portalURL, mac, timezone string) *StalkerClient {
	return &StalkerClient{
		PortalURL: portalURL,
		MAC:       mac,
		Timezone:  timezone,
	}
}

/* Authenticate performs the handshake action to obtain a Bearer token. */
func (c *StalkerClient) Authenticate() error {
	// Build API URL for handshake
	apiURL := fmt.Sprintf("%s/stalker_portal/server/load.php", c.PortalURL)
	params := url.Values{
		"type":          {"stb"},
		"action":        {"handshake"},
		"JsHttpRequest": {"1-xml"},
	}
	req, err := http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("failed to create handshake request: %w", err)
	}

	// Set headers to mimic STB
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))
	req.Header.Set("User-Agent", "Mozilla/5.0 (QtEmbedded; U; Linux; C)")

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("handshake request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var response HandshakeResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to parse handshake response: %w", err)
	}
	c.Token = response.Js.Token
	return nil
}

/* ProbeServer tests server capabilities (gzip support, create_link requirement). */
func (c *StalkerClient) ProbeServer() error {
	// Test gzip support
	apiURL := fmt.Sprintf("%s/stalker_portal/server/load.php", c.PortalURL)
	params := url.Values{
		"type":          {"itv"},
		"action":        {"get_all_channels"},
		"gzip":          {"true"},
		"JsHttpRequest": {"1-xml"},
	}
	req, _ := http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err == nil && resp.Header.Get("Content-Encoding") == "gzip" {
		c.Config.SupportsGzip = true
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Test create_link requirement
	params.Set("action", "create_link")
	params.Set("cmd", "test_channel")
	req, _ = http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))
	resp, err = client.Do(req)
	if err == nil && resp.StatusCode == 200 {
		var response CreateLinkResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err == nil && response.Js.Cmd != "" {
			c.Config.RequiresCreateLink = true
		}
	}
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

/* GetChannels fetches all channels, optionally using gzip compression. */
func (c *StalkerClient) GetChannels() ([]Channel, error) {
	// Authenticate if no token
	if c.Token == "" {
		if err := c.Authenticate(); err != nil {
			return nil, err
		}
	}

	// Build API URL for channels
	apiURL := fmt.Sprintf("%s/stalker_portal/server/load.php", c.PortalURL)
	params := url.Values{
		"type":          {"itv"},
		"action":        {"get_all_channels"},
		"JsHttpRequest": {"1-xml"},
	}
	if c.Config.SupportsGzip {
		params.Set("gzip", "true")
	}
	req, err := http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create channels request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))
	if c.Config.SupportsGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("channels request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle gzip compression
	var reader io.Reader = resp.Body
	if c.Config.SupportsGzip && resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	// Parse response
	var response ChannelListResponse
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse channels response: %w", err)
	}
	return response.Js.Channels, nil
}

/* GetPlaybackURL fetches the playback URL for a channel, using create_link if required. */
func (c *StalkerClient) GetPlaybackURL(channelCmd string) (string, error) {
	// Return direct URL if create_link is not required
	if !c.Config.RequiresCreateLink {
		return channelCmd, nil
	}

	// Authenticate if no token
	if c.Token == "" {
		if err := c.Authenticate(); err != nil {
			return "", err
		}
	}

	// Build API URL for create_link
	apiURL := fmt.Sprintf("%s/stalker_portal/server/load.php", c.PortalURL)
	params := url.Values{
		"type":           {"itv"},
		"action":         {"create_link"},
		"cmd":            {channelCmd},
		"forced_storage": {"undefined"},
		"disable_ad":     {"0"},
		"JsHttpRequest":  {"1-xml"},
	}
	req, err := http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create playback URL request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))
	req.Header.Set("User-Agent", "Mozilla/5.0 (QtEmbedded; U; Linux; C)")

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("playback URL request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var response CreateLinkResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to parse playback URL response: %w", err)
	}
	return response.Js.Cmd, nil
}

/* GetEPG fetches EPG data for a channel with timezone adjustment. */
func (c *StalkerClient) GetEPG(channelID string) ([]EPGProgram, error) {
	// Authenticate if no token
	if c.Token == "" {
		if err := c.Authenticate(); err != nil {
			return nil, err
		}
	}

	// Build API URL for EPG
	apiURL := fmt.Sprintf("%s/stalker_portal/server/load.php", c.PortalURL)
	params := url.Values{
		"type":          {"itv"},
		"action":        {"get_epg"},
		"ch_id":         {channelID},
		"JsHttpRequest": {"1-xml"},
	}
	req, err := http.NewRequest("GET", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create EPG request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Cookie", fmt.Sprintf("mac=%s; stb_lang=en; timezone=%s", c.MAC, c.Timezone))

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("EPG request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var epgResp EPGResponse
	if err := json.NewDecoder(resp.Body).Decode(&epgResp); err != nil {
		return nil, fmt.Errorf("failed to parse EPG response: %w", err)
	}

	// Adjust timestamps for timezone
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", c.Timezone, err)
	}
	for i, p := range epgResp.Js.Programs {
		epgResp.Js.Programs[i].Start = time.Unix(p.Start, 0).In(loc).Unix()
		epgResp.Js.Programs[i].Stop = time.Unix(p.Stop, 0).In(loc).Unix()
	}
	return epgResp.Js.Programs, nil
}

/* ConvertEPGToXMLTV converts EPG data to XMLTV format. */
func (c *StalkerClient) ConvertEPGToXMLTV(channelID string, programs []EPGProgram) (string, error) {
	// Create XMLTV structure
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return "", fmt.Errorf("invalid timezone %s: %w", c.Timezone, err)
	}
	xmltv := XMLTV{
		Channels: []XMLTVChannel{{ID: channelID, DisplayName: channelID}},
	}
	for _, p := range programs {
		startTime := time.Unix(p.Start, 0).In(loc).Format("20060102150405 -0700")
		stopTime := time.Unix(p.Stop, 0).In(loc).Format("20060102150405 -0700")
		xmltv.Programs = append(xmltv.Programs, XMLTVProgram{
			Start:    startTime,
			Stop:     stopTime,
			Channel:  p.ChannelID,
			Title:    p.Name,
			Desc:     p.Desc,
			Category: p.Category,
		})
	}

	// Marshal to XML
	output, err := xml.MarshalIndent(xmltv, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal XMLTV: %w", err)
	}
	return string(output), nil
}

/* DownloadChannelLogo downloads a channel logo to the specified directory with a custom filename format. */
func (c *StalkerClient) DownloadChannelLogo(logoURL, outputDir, filenameFormat string, channel Channel) error {
	// Validate logo URL
	if logoURL == "" {
		return fmt.Errorf("no logo URL provided for channel %s", channel.Name)
	}

	// Build full logo URL
	u, err := url.Parse(logoURL)
	if err != nil {
		return fmt.Errorf("invalid logo URL %s: %w", logoURL, err)
	}
	if !u.IsAbs() {
		u, err = url.Parse(fmt.Sprintf("%s/stalker_portal%s", c.PortalURL, logoURL))
		if err != nil {
			return fmt.Errorf("failed to construct logo URL: %w", err)
		}
	}

	// Download logo
	resp, err := http.Get(u.String())
	if err != nil {
		return fmt.Errorf("failed to download logo %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Format filename
	filename := filenameFormat
	filename = fmt.Sprintf(filename, channel.ID, channel.Name)
	filename = filepath.Join(outputDir, filename)

	// Save file
	out, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save logo %s: %w", filename, err)
	}
	return nil
}
```