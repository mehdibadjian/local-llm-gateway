"""
EmbedSvc gRPC server.

Loads all-MiniLM-L6-v2 once at startup and serves Embed + Health RPCs.
Concurrency is capped via EMBED_CONCURRENCY (default 4) to keep memory
below the 512 Mi container limit.
"""
import os
import logging
from concurrent import futures

import grpc
import embed_pb2
import embed_pb2_grpc
from sentence_transformers import SentenceTransformer

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

EMBED_CONCURRENCY = int(os.getenv("EMBED_CONCURRENCY", "4"))
PORT = int(os.getenv("EMBED_PORT", "50051"))

# Model is loaded once at import time; normalize_embeddings=True produces
# unit-length vectors ready for cosine-similarity search.
logger.info("Loading sentence-transformers model …")
_model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")
logger.info("Model loaded.")


class EmbedServicer(embed_pb2_grpc.EmbedServiceServicer):
    def Embed(self, request, context):
        vector = _model.encode([request.text], normalize_embeddings=True)[0].tolist()
        return embed_pb2.EmbedResponse(embedding=vector)

    def Check(self, request, context):
        return embed_pb2.HealthCheckResponse(status="SERVING")


def serve() -> None:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=EMBED_CONCURRENCY))
    embed_pb2_grpc.add_EmbedServiceServicer_to_server(EmbedServicer(), server)
    server.add_insecure_port(f"[::]:{PORT}")
    server.start()
    logger.info("EmbedSvc gRPC listening on :%d (concurrency=%d)", PORT, EMBED_CONCURRENCY)
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
