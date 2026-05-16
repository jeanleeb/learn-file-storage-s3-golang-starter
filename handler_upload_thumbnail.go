package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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

	const maxMemory = 10 << 20 // 10 MB
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	vidMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video metadata", err)
		return
	}

	if vidMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You don't have permission to upload a thumbnail for this video", nil)
		return
	}

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse media type", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type. Only JPEG and PNG are allowed", nil)
		return
	}

	mediaExt := mediaTypeToExt(mediaType)
	fileName, err := getRandFileName(mediaExt)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to generate file name", err)
		return
	}

	thumbFile, err := os.Create(cfg.getAssetDiskPath(fileName))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail file", err)
		return
	}
	defer thumbFile.Close()

	_, err = io.Copy(thumbFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing thumbnail file", err)
		return
	}

	thumbUrl := cfg.getAssetURL(fileName)

	updatedVideo := database.Video{ID: videoID, ThumbnailURL: &thumbUrl, VideoURL: vidMetadata.VideoURL, CreateVideoParams: database.CreateVideoParams{UserID: vidMetadata.UserID, Title: vidMetadata.Title, Description: vidMetadata.Description}}
	if err = cfg.db.UpdateVideo(updatedVideo); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video metadata with thumbnail info", err)
		return
	}

	fmt.Println("updated thumbnail for video", videoID, "by user", userID)

	jsonRes, err := json.Marshal(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to marshal JSON", err)
		return
	}

	respondWithJSON(w, http.StatusOK, jsonRes)
}
