"""
EmbedSvc HTTP server (Flask).

Exposes the same contract as the gRPC server but over plain HTTP for
easier local testing and for the Go HTTPEmbedClient:
  POST /embed  {"text": "..."} → {"embedding": [...]}
  GET  /health               → {"status": "SERVING"}
"""
import os
import logging

from flask import Flask, request, jsonify
from sentence_transformers import SentenceTransformer

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

PORT = int(os.getenv("PORT", os.getenv("EMBED_HTTP_PORT", "5001")))

logger.info("Loading sentence-transformers model …")
_model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")
logger.info("Model loaded.")

app = Flask(__name__)


@app.route("/embed", methods=["POST"])
def embed():
    body = request.get_json(force=True)
    if not body or "text" not in body:
        return jsonify({"error": "missing 'text' field"}), 400
    vector = _model.encode([body["text"]], normalize_embeddings=True)[0].tolist()
    return jsonify({"embedding": vector})


@app.route("/health")
def health():
    return jsonify({"status": "SERVING"})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=PORT)
