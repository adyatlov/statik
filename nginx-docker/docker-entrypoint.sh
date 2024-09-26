#!/bin/bash
set -e

# Clone the repository and checkout the specific commit
/usr/local/bin/clone_repo.sh

# Execute the original entrypoint script from NGINX
exec "$@"
