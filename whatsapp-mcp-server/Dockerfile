FROM python:3.12-slim

# Install ffmpeg for audio conversion (optional but recommended)
RUN apt-get update && apt-get install -y \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install uv
RUN pip install uv

# Copy uv.lock and pyproject.toml first for better caching
COPY uv.lock pyproject.toml ./

# Install dependencies using uv
RUN uv sync --frozen

# Copy source code
COPY . .

# Create non-root user for security
RUN useradd -m -u 1000 whatsapp && chown -R whatsapp:whatsapp /app
USER whatsapp

# Expose port for MCP server
EXPOSE 8081

CMD ["uv", "run", "main.py"]