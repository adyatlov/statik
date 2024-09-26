#!/bin/bash

# Check if the STATIK_REPO environment variable is set
if [ -z "$STATIK_REPO" ]; then
  echo "STATIK_REPO environment variable must be set."
  exit 1
fi

# Clean the /usr/share/nginx/html directory
rm -rf /usr/share/nginx/html/*

# Clone the repository
git clone "$STATIK_REPO" /usr/share/nginx/html

cd /usr/share/nginx/html

# If STATIK_COMMIT_HASH is set, checkout to the specific commit, otherwise use the latest commit (HEAD)
if [ -n "$STATIK_COMMIT_HASH" ]; then
  echo "Checking out commit $STATIK_COMMIT_HASH"
  git checkout "$STATIK_COMMIT_HASH"
else
  echo "STATIK_COMMIT_HASH is not set, using the latest commit (HEAD)"
  git checkout HEAD
fi

# Remove the .git directory to avoid exposing the repository
rm -rf /usr/share/nginx/html/.git
