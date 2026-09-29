// [SPEK] Kedua mock wajib memvalidasi kredensial masuk.
// [TIM] 401 bila header X-BMKG-Key tidak ada, 403 bila key-nya salah.

import type { FastifyReply, FastifyRequest } from "fastify";

export function requireBmkgKey(expectedKey: string) {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    const provided = req.headers["x-bmkg-key"];

    if (typeof provided !== "string") {
      reply.code(401).send({ error: "missing X-BMKG-Key header" });
      return;
    }
    if (provided !== expectedKey) {
      reply.code(403).send({ error: "invalid X-BMKG-Key" });
      return;
    }
  };
}
