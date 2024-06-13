package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/davecgh/go-spew/spew"

	"cloud.google.com/go/storage"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/api/option"
)

const OpenAIKey = `[REDACTED]`

const GoogleCloudServiceAccountKey = `{
 "type": "service_account",
 "project_id": "[REDACTED]",
 "private_key_id": "[REDACTED]",
 "private_key": "[REDACTED]",
 "client_email": "[REDACTED]",
 "client_id": "[REDACTED]",
 "auth_uri": "https://accounts.google.com/o/oauth2/auth",
 "token_uri": "https://oauth2.googleapis.com/token",
 "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
 "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/svc-controlplane-asset-manager%40storytell-development-f686.iam.gserviceaccount.com",
 "universe_domain": "googleapis.com"
}`

func main() {
	log.SetOutput(os.Stdout)

	if err := process(); err != nil {
		log.Fatalf("failed to process: %s", err)
	}
	log.Printf("done")
}

func process() error {
	ctx := context.Background()

	// Read from Google Cloud Storage
	seeker, size, err := ReadFromGoogleCloudStorage(ctx, "storytell-uc-assets-development", "organization_cpkar5i4jkdm4oidnv50/assets/asset_cpl24t24jkdveqruqd30.pdf")
	if err != nil {
		return err
	}

	// Read into memory
	buf, bytesRead, err := readIntoBuffer(seeker, size)
	if err != nil {
		return err
	}
	log.Printf("read %d of %d bytes into buffer", bytesRead, size)

	// Push to OpenAI
	fileID, err := pushToOpenAI(ctx, buf)
	if err != nil {
		return err
	}
	log.Printf("returned the file id %q from openai", fileID)

	// Now, let's stream the output and see what the summary of this file is...
	output, err := prompt(ctx, "Please summarize the file and tell me the email address of the user who uploaded it and when", fileID)
	if err != nil {
		return err
	}
	log.Printf("The final output was: %s", output)
	return nil
}

type RequestResponseDiagnostics struct {
	Request        openai.ChatCompletionRequest `json:"request,omitempty"`
	Response       interface{}                  `json:"response,omitempty"`
	StreamedBuffer string                       `json:"streamedBuffer,omitempty"`
}

func prompt(ctx context.Context, s string, fileID string) (string, error) {
	rrd := RequestResponseDiagnostics{}
	messages := []openai.ChatCompletionMessage{
		{
			Role:    "system",
			Content: fmt.Sprintf("File to use to help answer the question: %s", fileID),
			Name:    "system",
		},
		{
			Role:    "user",
			Content: s,
			Name:    "user",
		},
	}

	rrd.Request = openai.ChatCompletionRequest{
		Model:    "gpt-4o",
		User:     "bruce-wayne",
		Messages: messages,
		Stream:   true,
	}

	spew.Dump("REQUEST", rrd.Request)

	client := openai.NewClient(OpenAIKey)
	stream, err := client.CreateChatCompletionStream(ctx, rrd.Request)
	defer func() {
		_ = stream.Close()
	}()
	if err != nil {
		return "", err
	}

	sb := strings.Builder{}
	// No need to stream to a channel for this simple example... but I'll use the same technique.
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			spew.Dump("RESPONSE", rrd.Request)
			return sb.String(), nil
		}
		if err != nil {
			return "", fmt.Errorf("error receiving stream response: %w", err)
		}
		rrd.Response = response
		sb.WriteString(response.Choices[0].Delta.Content)
		if err != nil {
			return "", fmt.Errorf("error marshaling message: %w", err)
		}
		// Would streamt to channel here...
	}
}

func readIntoBuffer(seeker io.ReadSeeker, size int) ([]byte, int, error) {
	buf := make([]byte, size)
	bytesRead, err := seeker.Read(buf)
	if err != nil {
		return nil, 0, err
	}
	if bytesRead == 0 {
		return nil, 0, fmt.Errorf("failed to read any bytes into buffer")
	}
	if buf[0] == 0 {
		return nil, 0, fmt.Errorf("buffer is empty")
	}
	return buf, bytesRead, nil
}

func pushToOpenAI(ctx context.Context, buf []byte) (string, error) {
	client := openai.NewClient(OpenAIKey)
	req := openai.FileBytesRequest{
		Name:    "file.pdf",
		Bytes:   buf,
		Purpose: openai.PurposeAssistants,
	}
	resp, err := client.CreateFileBytes(ctx, req)
	// spew.Dump("OPEN AI FILE UPLOAD RESPONSE", resp)
	return resp.ID, err
}

func ReadFromGoogleCloudStorage(ctx context.Context, bucket, object string) (io.ReadSeeker, int, error) {
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(GoogleCloudServiceAccountKey)))
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = client.Close()
	}()
	buck := client.Bucket(bucket)
	o := buck.Object(object)
	reader, err := o.NewReader(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create new reader for object %s: %w", object, err)
	}
	defer func() {
		_ = reader.Close()
	}()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read data from GCS object: %w", err)
	}

	return io.ReadSeeker(bytes.NewReader(data)), len(data), nil
}
