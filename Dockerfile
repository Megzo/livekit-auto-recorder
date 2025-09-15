# Step 1: Build the application
FROM golang:1.24.6-alpine AS builder

WORKDIR /app

# Copy Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
# CGO_ENABLED=0 is important for creating a static binary
# -ldflags="-w -s" strips debug symbols to reduce binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /auto-recorder .

# Step 2: Create a minimal final image
FROM alpine:3.18

WORKDIR /

# Copy only the compiled binary from the builder stage
COPY --from=builder /auto-recorder /auto-recorder

# Expose the port the app runs on
EXPOSE 8080

# Command to run the application
CMD ["/auto-recorder"]
