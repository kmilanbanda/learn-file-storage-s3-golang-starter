package main

import (
	"net/http"
	"os"
	"os/exec"
	"io"
	"mime"
	"context"
	"fmt"
	"encoding/json"
	"bytes"
	"path"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoID, err := getVideoID(w, r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't verify video owner", err)
		return
	}
	if video.CreateVideoParams.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", err)
		return
	}

	const maxMemory = 1 << 30
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest,  "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Wrong format", err)	
		return
	}
	
	tmpFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create temp file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to copy to temp file", err)
		return
	}
	tmpFile.Seek(0, io.SeekStart)

	prefix := ""
	aspectRatio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video aspect ratio", err)
		return
	}
	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	processedFileName, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to process video", err)
		return
	}

	processedFile, err := os.Open(processedFileName)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to process video", err)
		return	
	}
	defer processedFile.Close()

	key := getAssetPath(mediaType)
	key = path.Join(prefix, key)
	putObjectInput := s3.PutObjectInput{
		Bucket:		aws.String(cfg.s3Bucket),
		Key:		aws.String(key),
		Body:		processedFile,
		ContentType:	aws.String(mediaType),
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &putObjectInput)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to put object in bucket", err)
		return
	}

	videoURL := "https://" + path.Join(cfg.s3CfDistribution, key)
	video.VideoURL = new(string)
	*video.VideoURL = videoURL 
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update internal video data", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	respondWithJSON(w, http.StatusOK, video)
}

func getVideoID(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)
	videoIDString := r.PathValue("videoID")	
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		return uuid.Nil, err
	}
	return videoID, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("/usr/bin/ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var videoAspectData struct{
		Streams []struct{
			Height	float64 `json:"height"`
			Width	float64 `json:"width"`
		}	`json:"streams"`
	}
	if err = json.Unmarshal(buffer.Bytes(), &videoAspectData); err != nil {
		return "", err
	}

	if len(videoAspectData.Streams) < 1 {
		return "", fmt.Errorf("No streams found")
	}	
	if videoAspectData.Streams[0].Width == 0 || videoAspectData.Streams[0].Height == 0 {
		return "", fmt.Errorf("Cannot have width or height of 0")
	}
	aspectRatio := calcAspectRatio(videoAspectData.Streams[0].Width, videoAspectData.Streams[0].Height)

	return aspectRatio, nil
}

func calcAspectRatio(width, height float64) (string) {
	ratio := width / height
	if ratio >= 1.77 && ratio <= 1.79 {
		return "16:9"
	} else if ratio >= 0.55 && ratio <= 0.57 {
		return "9:16"
	} else {
		return "other"
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputFilePath, nil
}


