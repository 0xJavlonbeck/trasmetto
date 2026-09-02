package client

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxUploadErrorBytes = 64 << 10

type UploadOptions struct {
	URL         string
	FilePath    string
	InsecureTLS bool
	Auth        string
	Stdout      io.Writer
	HTTPClient  *http.Client
}

func Upload(opts UploadOptions) error {
	parsed, err := parseRemoteURL(opts.URL)
	if err != nil {
		return err
	}

	file, err := os.Open(opts.FilePath)
	if err != nil {
		return fmt.Errorf("open upload file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("inspect upload file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return fmt.Errorf("%s is not a regular file", opts.FilePath)
	}

	requestBody, uploadBody := io.Pipe()
	multipartWriter := multipart.NewWriter(uploadBody)
	request, err := http.NewRequest(http.MethodPost, parsed.String(), requestBody)
	if err != nil {
		_ = file.Close()
		_ = requestBody.Close()
		_ = uploadBody.Close()
		return err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("User-Agent", "trasmetto")
	applyAuth(request, parsed, opts.Auth)

	uploadDone := make(chan error, 1)
	go streamUploadFile(file, filepath.Base(info.Name()), multipartWriter, uploadBody, uploadDone)

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient(opts.InsecureTLS)
	}
	response, requestErr := httpClient.Do(request)
	if requestErr != nil {
		_ = requestBody.CloseWithError(requestErr)
		<-uploadDone
		return requestErr
	}
	defer response.Body.Close()

	_ = requestBody.Close()
	uploadErr := <-uploadDone
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return uploadResponseError(response)
	}
	if uploadErr != nil {
		return fmt.Errorf("read upload file: %w", uploadErr)
	}

	if opts.Stdout != nil {
		fmt.Fprintf(opts.Stdout, "uploaded %s -> %s\n", opts.FilePath, parsed.String())
	}
	return nil
}

func streamUploadFile(file *os.File, filename string, writer *multipart.Writer, body *io.PipeWriter, done chan<- error) {
	defer file.Close()

	part, err := writer.CreateFormFile("files", filename)
	if err == nil {
		_, err = io.Copy(part, file)
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = body.CloseWithError(err)
	} else {
		_ = body.Close()
	}
	done <- err
}

func uploadResponseError(response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUploadErrorBytes))
	if err != nil {
		return fmt.Errorf("upload failed: HTTP %d", response.StatusCode)
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		return fmt.Errorf("upload failed: HTTP %d", response.StatusCode)
	}
	return fmt.Errorf("upload failed: HTTP %d: %s", response.StatusCode, message)
}
