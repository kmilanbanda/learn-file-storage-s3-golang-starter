package main

import (
	"io"
	"fmt"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
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


	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Unable to get Content-Type header", err)
		return
	} else if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Incompatible file type", err)
		return
	}	

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video", err)
		return
	}
	if video.CreateVideoParams.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not owner of this video", err)
		return
	}

	assetPath := getAssetPath(mediaType)
	outputFile, err := os.Create(cfg.getAssetDiskPath(assetPath))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create thumbnail file", err)
		return
	}
	if _, err := io.Copy(outputFile, file); err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create thumbnail file", err)
		return
	}

	thumbnailURL := cfg.getAssetURL(assetPath)

	video.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
