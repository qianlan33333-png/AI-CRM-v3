import unittest
from release_queue import change
class QueueTests(unittest.TestCase):
 def setUp(self):
  self.q={'items':[]}
  for cid in ['a','b']:change(self.q,'enqueue',manifest={'candidate_id':cid,'base_main_sha':'base'})
 def transition(self,cid,status,main='base'):change(self.q,'transition',candidate_id=cid,status=status,main=main)
 def test_fifo(self):
  with self.assertRaises(ValueError):self.transition('b','preview_building')
 def test_single_owner(self):
  self.transition('a','preview_building')
  with self.assertRaises(ValueError):self.transition('b','preview_building')
 def test_no_skipping(self):
  with self.assertRaises(ValueError):self.transition('a','production')
 def test_stale_main(self):
  with self.assertRaises(ValueError):self.transition('a','preview_building','changed')
  self.transition('a','stale_candidate');self.transition('b','preview_building')
 def test_merged_keeps_ownership(self):
  for s in ['preview_building','staging_acceptance','frozen','waiting_merge','merged']:self.transition('a',s)
  with self.assertRaises(ValueError):self.transition('b','preview_building')
if __name__=='__main__':unittest.main()
