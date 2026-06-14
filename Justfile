# --- Variables ---

project_id  := "apex-494315"
region      := "asia-south1"
registry    := "asia-south1-docker.pkg.dev"
repo        := "prj-apex-artifact-registry"
service     := "probe-service"
sa          := "ci-cd-98@apex-494315.iam.gserviceaccount.com"
image       := registry + "/" + project_id + "/" + repo + "/" + service
git_sha     := `git rev-parse --short HEAD`

spanner_db       := "projects/apex-494315/instances/apex-spanner-instance/databases/apex-database"
subscription     := "projects/apex-494315/subscriptions/sub-probe"
dlq_subscription := "projects/apex-494315/subscriptions/sub-probe-dlq"

# --- Local Dev ---

spanner-up:
  docker run --name spanner-emulator --network spanner-net \
    -p 9010:9010 -p 9020:9020 -d \
    gcr.io/cloud-spanner-emulator/emulator

emulator-config:
  gcloud config configurations create emulator 2>/dev/null || true
  gcloud config configurations activate emulator
  gcloud config set auth/disable_credentials true
  gcloud config set project test-project
  gcloud config set api_endpoint_overrides/spanner http://localhost:9020/

spanner-create:
  gcloud spanner instances create test-instance \
    --config=regional-us-central1 \
    --description="Local Instance" \
    --nodes=1
  gcloud spanner databases create test-database --instance test-instance

spanner-cli:
  SPANNER_EMULATOR_HOST=localhost:9010 spanner-cli sql \
    --project test-project \
    --instance test-instance \
    --database test-database

pubsub-up:
  gcloud beta emulators pubsub start \
    --project=test-project \
    --host-port=0.0.0.0:8085 &
  sleep 2
  echo "Pub/Sub emulator ready on 0.0.0.0:8085"

pubsub-init:
  curl -X PUT http://localhost:8085/v1/projects/test-project/topics/test-topic
  curl -X PUT http://localhost:8085/v1/projects/test-project/subscriptions/test-subscription \
    -H "Content-Type: application/json" \
    -d '{"topic": "projects/test-project/topics/test-topic"}'

pubsub-down:
  lsof -ti tcp:8085 | xargs kill -9 2>/dev/null; true

run:
  docker run -d \
    -v $HOME/.config/gcloud:/tmp/gcloud:ro \
    -e GOOGLE_APPLICATION_CREDENTIALS=/tmp/gcloud/application_default_credentials.json \
    -e OTEL_RESOURCE_ATTRIBUTES="gcp.project_id={{project_id}}" \
    -e OTEL_EXPORTER_OTLP_ENDPOINT=https://telemetry.googleapis.com \
    -e GOOGLE_CLOUD_QUOTA_PROJECT="{{project_id}}" \
    --name prj-apex-probe-service \
    --network spanner-net \
    -p 8080:8080 \
    -e SPANNER_EMULATOR_HOST=spanner-emulator:9010 \
    -e PUBSUB_EMULATOR_HOST=host.docker.internal:8085 \
    -e APP_ENV=local \
    prj-apex-probe-service

# --- Build & Push ---

docker-auth:
  gcloud auth configure-docker {{registry}}

build:
  docker buildx build --platform=linux/amd64 --load \
    -t prj-apex-probe-service \
    .

push:
  docker buildx build --platform=linux/amd64 --push \
    -t {{image}}:{{git_sha}} \
    -t {{image}}:latest \
    .

# --- Cloud Run Deploy (temporary — migrate to GKE later) ---

deploy: push
  gcloud run deploy {{service}} \
    --image={{image}}:{{git_sha}} \
    --region={{region}} \
    --platform=managed \
    --no-allow-unauthenticated \
    --service-account={{sa}} \
    --memory=512Mi --cpu=1 \
    --min-instances=1 --max-instances=10 \
    --concurrency=80 --timeout=300 \
    --clear-secrets \
    --set-env-vars="APP_ENV=development,SERVICE={{service}},REGION={{region}},PROJECT_ID={{project_id}},SPANNER_DATABASE={{spanner_db}},SUBSCRIPTION={{subscription}},DLQ_SUBSCRIPTION={{dlq_subscription}},OTEL_RESOURCE_ATTRIBUTES=gcp.project_id={{project_id}},OTEL_EXPORTER_OTLP_ENDPOINT=https://telemetry.googleapis.com,GOOGLE_CLOUD_QUOTA_PROJECT={{project_id}}"

# --- Code Quality ---

fmt:
  go fmt ./...

vet:
  go vet ./...

lint:
  golangci-lint run

test:
  go test ./... -v
