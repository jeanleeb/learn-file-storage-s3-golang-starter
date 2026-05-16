package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30 // 1GB
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	if err := r.ParseMultipartForm(uploadLimit); err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large, should be under 1GB", err)
	}

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

	vidMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video metadata", err)
		return
	}

	if vidMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You don't have permission to upload a thumbnail for this video", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse from file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type. Only mp4 is supported.", err)
		return
	}

	mediaExt := mediaTypeToExt(mediaType)
	fmt.Printf("Got mediaExt %s\n", mediaExt)
	tempFilePath := fmt.Sprintf("/tmp/%s", "tubely-upload."+mediaExt)

	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating temp video file for upload", err)
		return
	}
	defer os.Remove(tempFilePath)
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing temp video file", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reseting temp video file pointer", err)
		return
	}

	processedPath, err := processVideoForFastStart(tempFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video", err)
		return
	}

	fileName, err := getRandFileName(mediaExt)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to generate file name", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tempFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video aspect ratio", err)
		return
	}

	switch aspectRatio {
	case "16:9":
		fileName = "landscape/" + fileName
	case "9:16":
		fileName = "portrait/" + fileName
	default:
		fileName = "other/" + fileName
	}

	processedFile, err := os.Open(processedPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading processed video file", err)
		return
	}
	defer os.Remove(processedPath)
	defer processedFile.Close()

	fmt.Printf("Uploading %s to S3 with content type %s", fileName, mediaType)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{Bucket: &cfg.s3Bucket, Key: &fileName, Body: processedFile, ContentType: &mediaType})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, fileName)

	updatedVideo := database.Video{
		ID:                videoID,
		VideoURL:          &videoURL,
		ThumbnailURL:      vidMetadata.ThumbnailURL,
		CreatedAt:         vidMetadata.CreatedAt,
		UpdatedAt:         time.Now(),
		CreateVideoParams: database.CreateVideoParams{UserID: userID, Title: vidMetadata.Title, Description: vidMetadata.Description},
	}
	err = cfg.db.UpdateVideo(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving updated video URL", err)
		return
	}

	presigned, err := cfg.dbVideoToSignedVideo(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generating presigned URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, presigned)
}
