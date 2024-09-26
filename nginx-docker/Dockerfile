FROM nginx:latest

# Install git to clone the repository
RUN apt-get update && apt-get install -y git && rm -rf /var/lib/apt/lists/*

# Copy the entrypoint and repo cloning scripts into the container
COPY clone_repo.sh /usr/local/bin/clone_repo.sh
COPY docker-entrypoint.sh /docker-entrypoint.sh

# Make the clone script executable
RUN chmod +x /usr/local/bin/clone_repo.sh

# Use the custom entrypoint script
ENTRYPOINT ["/docker-entrypoint.sh"]

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]