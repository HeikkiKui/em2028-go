import datetime
import http.server
import json
import re

import os
HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
ROOT = os.path.join(REPO, "web")
CSP = ("default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; "
       "font-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")

src = open(os.path.join(REPO, "db.go")).read()
SEED = []
for m in re.finditer(r'\{(\d+), "(\w+)", "(\w*)", "(\w*)", "(\w*)", "([^"]*)", "([^"]*)", (\d+), (\d+), "([^"]*)"\}', src):
    n, stage, grp, home, away, hs, as_, mo, d, venue = m.groups()
    SEED.append(dict(n=int(n), stage=stage, grp=grp, home=home, away=away, hs=hs, as_=as_, mo=int(mo), d=int(d), venue=venue))
assert len(SEED) == 51, len(SEED)


def ts(mo, d):
    return int(datetime.datetime(2028, mo, d, tzinfo=datetime.timezone.utc).timestamp())


STATE = {"logged": False, "preds": {}, "verified": False, "locked": False, "admin": False, "mfa": "off", "reminders": True,
         "orders": {}, "chat_reports": 0, "matches_put": []}
NAMES = {"A1": "Wales", "B1": "Englanti", "D1": "Pohjois-Irlanti", "E1": "Irlanti", "F1": "Skotlanti"}


def team(c):
    return {"code": c, "name": NAMES.get(c, c)} if c else None


def bracket():
    ms = []
    for s in SEED:
        p = STATE["preds"].get(s["n"])
        grp = s["stage"] == "group"
        ms.append({
            "n": s["n"], "stage": s["stage"], "group": s["grp"], "venue": s["venue"],
            "kickoff": ts(s["mo"], s["d"]), "kickoffConfirmed": False,
            "home": team(s["home"]) if grp else None, "away": team(s["away"]) if grp else None,
            "homeSlot": s["hs"], "awaySlot": s["as_"],
            "prediction": ({"homeGoals": p[0], "awayGoals": p[1]} if p else None), "result": None,
        })
    groups = {}
    for g in "ABCDEF":
        groups[g] = {"complete": False, "table": [
            dict(code="%s%d" % (g, i), name=NAMES.get("%s%d" % (g, i), "%s%d" % (g, i)),
                 played=0, won=0, drawn=0, lost=0, gf=0, ga=0, points=0) for i in range(1, 5)]}
    if STATE["locked"]:
        # täysi kaavio: ennustetut pudotuspelit ja yksi oikea tulos
        codes = ["A1","B2","C1","D2","E1","F2","B1","A2","C2","D1","E2","F1","A3","B3","C3","D3"]
        k = 0
        for m in ms:
            if m["stage"] == "group":
                m["prediction"] = {"homeGoals": 2, "awayGoals": 1}
                m["result"] = {"homeGoals": 2, "awayGoals": 1} if m["n"] <= 3 else None
                if m["result"]: m["points"] = 3
            else:
                m["home"], m["away"] = team(codes[(k*2) % 16]), team(codes[(k*2+1) % 16]); k += 1
                m["prediction"] = {"homeGoals": 1, "awayGoals": 0, "advance": "home"} if m["n"] != 40 else {"homeGoals": 1, "awayGoals": 1, "advance": "away"}
                if m["n"] == 37:
                    m["actualHome"], m["actualAway"] = m["home"], m["away"]; m["result"] = {"homeGoals": 1, "awayGoals": 0, "advance": "home"}; m["points"] = 4
    return {"readonly": False, "scorerPick": "Harry Kane", "champion": team("A1") if STATE["locked"] else None,
            "locked": STATE["locked"], "lockAt": ts(6, 9), "lockConfirmed": False, "predicted": len(STATE["preds"]), "total": 51,
            "groups": groups, "matches": ms,
            "score": {"total": 0, "exact": 0, "scorer": 0, "advancement": 0, "rounds": {}}}


class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=ROOT, **k)

    def end_headers(self):
        self.send_header("Content-Security-Policy", CSP)
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        p = self.path.split("?")[0]
        if not p.startswith("/api/"):
            return super().do_GET()
        if not STATE["logged"]:
            return self._json(401, {"error": "Kirjaudu sisään"})
        if p == "/api/me":
            return self._json(200, {"nickname": "Testaaja", "emailVerified": STATE["verified"], "reminders": STATE["reminders"]})
        if p == "/api/bracket":
            return self._json(200, bracket())
        if p == "/api/scorer":
            return self._json(200, {"player": "", "locked": False})
        if p == "/api/_lock":
            STATE["locked"] = True
            return self._json(200, {"ok": True})
        if p == "/api/leaderboard":
            es = [dict(rank=1, nickname="Maija", points=40, exact=5, advancement=20, scorerPoints=0, delta=2, you=False),
                  dict(rank=2, nickname="Testaaja", points=33, exact=4, advancement=14, scorerPoints=0, delta=-1, you=True),
                  dict(rank=3, nickname="Pekka", points=30, exact=3, advancement=12, scorerPoints=0, you=False)]
            return self._json(200, {"entries": es, "total": 3, "you": es[1]})
        if p.startswith("/api/players/"):
            if not STATE["locked"]:
                return self._json(403, {"error": "Muiden veikkaukset näkyvät vasta, kun turnaus on alkanut"})
            b = bracket(); b["readonly"] = True; b["player"] = "Maija"
            return self._json(200, b)
        if p == "/api/me/export":
            return self._json(200, {"nickname": "Testaaja", "email": "testi@example.com", "predictions": []})
        if p == "/api/chat":
            return self._json(200, [{"id": 1, "nickname": "Maija", "body": "Moi kaikki!", "at": 1800000000, "mine": False}])
        if p == "/api/matches":
            return self._json(200, [dict(n=x["n"], stage=x["stage"], group=x["grp"], home=team(x["home"]) if x["stage"]=="group" else None,
                away=team(x["away"]) if x["stage"]=="group" else None, homeSlot=x["hs"], awaySlot=x["as_"], venue=x["venue"],
                kickoff=ts(x["mo"], x["d"]) - 10**8, kickoffConfirmed=True, locked=True, open=False, result=None, prediction=None) for x in SEED])
        if p.startswith("/api/admin/") and not STATE["admin"]:
            return self._json(403, {"error": "Ei oikeuksia"})
        if p == "/api/admin/me":
            m = STATE["mfa"]
            return self._json(200, {"nickname": "Admin", "perms": ["results","setup","moderate","audit"], "secondsLeft": 40000,
                "mfa": {"enabled": m in ("on","verified"), "verified": m != "on", "required": False}})
        if p.startswith("/api/admin/") and STATE["mfa"] == "on":
            return self._json(401, {"error": "Syötä kaksivaiheisen kirjautumisen koodi", "code": "mfa"})
        if p == "/api/admin/overview":
            return self._json(200, {"users": 3, "predictors": 2, "predictions": 40, "chat": 1, "finished": 3, "muted": 0})
        if p == "/api/admin/users":
            return self._json(200, [dict(id=2, nickname="Maija", createdAt=1790000000, muted=False, predictions=51, admin=False),
                                    dict(id=3, nickname="Pekka", createdAt=1791000000, muted=True, predictions=12, admin=False),
                                    dict(id=1, nickname="Admin", createdAt=1780000000, muted=False, predictions=0, admin=True)])
        if p == "/api/admin/teams":
            return self._json(200, [dict(code="%s%d" % (g, i), name=NAMES.get("%s%d" % (g, i), "%s%d" % (g, i))) for g in "ABCDEF" for i in range(1, 5)])
        if p == "/api/admin/bracket":
            groups = {}
            for g in "ABCDEF":
                order = STATE["orders"].get(g) or ["%s1" % g, "%s2" % g, "%s3" % g, "%s4" % g]
                groups[g] = {"complete": True, "unresolved": g == "A" and g not in STATE["orders"], "order": order,
                    "table": [dict(code=c, played=3, won=1, drawn=1, lost=1, gf=3, ga=3, points=4) for c in order]}
            return self._json(200, {"groups": groups, "thirds": {"unresolved": True}, "r16": {}, "groupOverrides": list(STATE["orders"].keys())})
        if p == "/api/admin/chat":
            return self._json(200, [dict(id=1, userId=2, nickname="Maija", body="Moi kaikki!", at=1800000000, muted=False, admin=False, reports=2)])
        if p == "/api/admin/chat/blocklist":
            return self._json(200, {"words": ["kakka"]})
        if p == "/api/admin/admins":
            return self._json(200, [dict(nickname="Admin", perms=["results","setup","moderate","audit"])])
        if p == "/api/admin/log":
            return self._json(200, [])
        return self._json(404, {"error": "ei löydy"})

    def do_POST(self):
        b = self._body()
        p = self.path
        if p == "/api/register":
            STATE["logged"] = True
            return self._json(201, {"nickname": b["nickname"], "emailVerified": False})
        if p == "/api/login":
            STATE["logged"] = True
            return self._json(200, {"nickname": "Testaaja", "emailVerified": True})
        if p == "/api/logout":
            STATE["logged"] = False
            self.send_response(204)
            self.end_headers()
            return
        if p == "/api/me/reminders": STATE["reminders"] = b["enabled"]; return self._json(200, {"ok": True})
        if p == "/api/unsubscribe": return self._json(200, {"ok": True})
        if p.startswith("/api/chat/") and p.endswith("/report"): STATE["chat_reports"] += 1; return self._json(200, {"ok": True})
        if p == "/api/_admin": STATE["admin"] = True; STATE["mfa"] = b.get("mfa", "off"); return self._json(200, {"ok": True})
        if p == "/api/admin/mfa":
            if b.get("code") == "123456": STATE["mfa"] = "verified"; return self._json(200, {"ok": True})
            return self._json(400, {"error": "Koodi ei täsmää"})
        if p == "/api/admin/totp/setup": return self._json(200, {"secret": "JBSWY3DPEHPK3PXP", "uri": "otpauth://totp/EM2028:Admin?secret=JBSWY3DPEHPK3PXP&issuer=EM2028"})
        if p == "/api/admin/totp/confirm":
            if b.get("code") == "123456": STATE["mfa"] = "verified"; return self._json(200, {"ok": True})
            return self._json(400, {"error": "Koodi ei täsmää"})
        if p == "/api/email/resend":
            return self._json(200, {"ok": True})
        return self._json(404, {"error": "ei löydy"})

    def do_PUT(self):
        b = self._body()
        m = re.match(r"/api/predictions/(\d+)", self.path)
        if m:
            STATE["preds"][int(m.group(1))] = (b["homeGoals"], b["awayGoals"])
            return self._json(200, {"ok": True})
        m = re.match(r"/api/admin/groups/(\w)/order", self.path)
        if m: STATE["orders"][m.group(1)] = b["order"]; return self._json(200, {"ok": True})
        m = re.match(r"/api/admin/matches/(\d+)", self.path)
        if m: STATE["matches_put"].append((int(m.group(1)), b)); return self._json(200, {"ok": True})
        if self.path == "/api/admin/chat/blocklist": return self._json(200, {"ok": True, "words": b["words"]})
        return self._json(404, {"error": "ei löydy"})

    def log_message(self, *a):
        pass


def reset():
    STATE.update({"logged": False, "preds": {}, "verified": False, "locked": False, "admin": False, "mfa": "off",
                  "reminders": True, "orders": {}, "chat_reports": 0, "matches_put": []})


def serve(port=8099):
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", port), H)
    import threading
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


if __name__ == "__main__":
    http.server.ThreadingHTTPServer(("127.0.0.1", 8099), H).serve_forever()
