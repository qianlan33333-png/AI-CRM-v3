import base64
import io
import json
import tempfile
import unittest
from datetime import datetime, timezone, timedelta
from pathlib import Path
from openpyxl import Workbook
from batches import Handler, Source, Service, Invalid, HEADERS, parse_excel, content_key

PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=')

def workbook(rows, header=HEADERS):
    book = Workbook(); sheet = book.active; sheet.append(header)
    for row in rows: sheet.append(row)
    out = io.BytesIO(); book.save(out); return out.getvalue()

class FakeSource:
    def __init__(self): self.events = {}; self.groups = {}; self.cards = {}
    def card(self, path): return self.cards.get(path)
    def segment(self, unionid): return self.groups.get(unionid, 'unknown')
    def opens(self, unionid, path, start, end): return self.events.get(unionid)

class Tests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name); (self.root/'cover.png').write_bytes(PNG)
        self.source = FakeSource()
        self.config = {'appid':'fixture-app','default_title':'完整内容','default_cover':str(self.root/'cover.png')}
        self.service = Service(self.root/'data.sqlite', self.config, self.source)
        self.raw = workbook([['00123','宝子，本周的案例','pages/article/article?lesson_id=12','staff-1']])
    def test_exact_text_and_replay_across_restart(self):
        first = self.service.import_file(self.raw)
        self.assertEqual(first['rows'][0]['unionid'], '00123')
        self.assertEqual(first['rows'][0]['card']['title'],'完整内容')
        self.service.link(first['batch_key'],81)
        restarted = Service(self.root/'data.sqlite',self.config,self.source)
        replay = restarted.import_file(self.raw)
        self.assertEqual(replay['batch_key'],first['batch_key']); self.assertEqual(replay['plan_id'],81)
        new = restarted.import_file(self.raw,True,'explicit-new-batch')
        self.assertNotEqual(first['batch_key'],new['batch_key'])
        self.assertEqual(new['batch_key'],restarted.import_file(self.raw,True,'explicit-new-batch')['batch_key'])
    def test_reject_numeric_formula_duplicate_and_wrong_headers(self):
        for rows in [[[123,'x','pages/a/a','s']], [['u','=NOW()','pages/a/a','s']], [['u','x','pages/a/a','s'],['u','y','pages/b/b','s']]]:
            with self.subTest(rows=rows),self.assertRaises(Invalid):parse_excel(workbook(rows))
        with self.assertRaises(Invalid):parse_excel(workbook([],['user','copy','path','sender']))
    def test_matched_card_and_bad_cover_fallback(self):
        path='pages/article/article?lesson_id=12'
        self.source.cards[path]={'title':'案例标题','cover_base64':base64.b64encode(PNG).decode()}
        self.assertEqual(self.service.card(path)['title'],'案例标题')
        self.source.cards[path]['cover_base64']='broken'
        self.assertEqual(self.service.card(path)['title'],'完整内容')
    def test_individual_windows_dedup_late_events_and_missing_source(self):
        start=datetime(2026,9,8,tzinfo=timezone.utc)
        rows=[{'id':1,'unionid':'u1','path':'pages/article/article?lesson_id=12','state':'delivery_proven','sent_at':start.isoformat()},
              {'id':2,'unionid':'u2','path':'pages/article/article?lesson_id=12','state':'delivery_proven','sent_at':(start+timedelta(hours=20)).isoformat()},
              {'id':3,'unionid':'u3','path':'pages/unknown/index','state':'delivery_proven','sent_at':start.isoformat()},
              {'id':4,'unionid':'u4','path':'pages/article/article?lesson_id=12','state':'provider_accepted','sent_at':None}]
        self.source.groups={'u1':'A','u2':'B'}
        self.service.snapshot('approval1',rows)
        self.source.groups['u1']='D' # Classification must stay frozen.
        self.source.events={'u1':[(start+timedelta(hours=11)).isoformat()]*3,'u2':[]}
        report=self.service.observe(1,'approval1',rows,(start+timedelta(hours=25)).isoformat())
        self.assertEqual(report['windows']['12']['A']['opened'],1)
        self.assertEqual(report['windows']['24']['A']['open_rate'],1)
        self.assertEqual(report['windows']['12']['B']['observing'],1)
        self.assertEqual(report['windows']['24']['unknown']['unavailable'],1)
        self.assertIsNone(report['windows']['24']['unknown']['open_rate'])
        self.assertEqual(report['windows']['48']['A']['observing'],1)
        self.assertEqual(sum(g['sent'] for g in report['windows']['24'].values()),3)
        self.source.events['u2']=[(start+timedelta(hours=35)).isoformat()]
        late=self.service.observe(1,'approval1',rows,(start+timedelta(hours=70)).isoformat())
        self.assertEqual(late['windows']['12']['B']['open_rate'],0)
        self.assertEqual(late['windows']['24']['B']['open_rate'],1)
        self.assertEqual(late['windows']['48']['B']['open_rate'],1)
        self.assertEqual(self.service.report(1)['updated_at'],late['updated_at'])
    def test_authenticated_http_import_and_native_plan_link(self):
        import threading
        from http.server import ThreadingHTTPServer
        from urllib.request import Request, urlopen
        from urllib.error import HTTPError
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        server.service = Service(self.root/'http.sqlite', self.config)
        server.token = 'fixture-' * 8
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            base = 'http://127.0.0.1:' + str(server.server_port)
            with self.assertRaises(HTTPError) as rejected:
                urlopen(base + '/health')
            self.assertEqual(rejected.exception.code, 401)
            def call(path, body=None):
                request = Request(base + path, data=body, headers={'Authorization': 'Bearer ' + server.token})
                with urlopen(request) as response: return response.read()
            first = json.loads(call('/imports', self.raw))
            self.assertEqual(first['rows'][0]['unionid'], '00123')
            call('/link', json.dumps({'batch_key': first['batch_key'], 'plan_id': 123}).encode())
            replay = json.loads(call('/imports', self.raw))
            self.assertTrue(replay['replayed'])
            self.assertEqual(replay['plan_id'], 123)
            self.assertEqual(call('/covers/' + first['rows'][0]['card']['cover_digest']), PNG)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_empty_events_require_explicit_collection_coverage(self):
        source = Source({})
        complete = [None]
        def query(name, params):
            if name == 'coverage_sql':
                return [] if complete[0] is None else [{'complete': complete[0]}]
            if name == 'user_sql': return [{'user_id': 7}]
            return []
        source.query = query
        now = datetime.now(timezone.utc)
        args = ('u', 'pages/article/article?lesson_id=1', now, now)
        self.assertIsNone(source.opens(*args))
        complete[0] = 0
        self.assertIsNone(source.opens(*args))
        complete[0] = 1
        self.assertEqual(source.opens(*args), [])

    def test_path_only_known_content_routes(self):
        self.assertEqual(content_key('pages/article/article?lesson_id=55&from=learn'),('lesson','55'))
        self.assertIsNone(content_key('pages/home/index'))
        self.assertIsNone(content_key('pages/article/article?lesson_id=1&lesson_id=2'))

if __name__=='__main__': unittest.main()
