#!/usr/bin/env python3
"""Tests for check-evm-layouts.py. A looser layout check is the risk, so every way to break a layout
must still fail it, and only carving from the trailing gap may pass."""

import importlib.util
import json
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("layouts", os.path.join(HERE, "check-evm-layouts.py"))
layouts = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layouts)


def entry(label, slot, typ, offset=0):
    return {"label": label, "slot": str(slot), "offset": offset, "type": typ}


def gap(slot, length):
    return entry("__gap", slot, f"t_array(t_uint256){length}_storage")


BASE = [
    entry("_delay", 0, "t_uint48"),
    entry("_count", 1, "t_uint256"),
    entry("_ops", 2, "t_mapping(t_uint256,t_struct(Operation)2345_storage)"),
    gap(3, 40),
]


class CheckEVMLayouts(unittest.TestCase):
    def check(self, current, baseline=BASE):
        with tempfile.TemporaryDirectory() as d:
            paths = []
            for name, storage in (("baseline", baseline), ("current", current)):
                path = os.path.join(d, name + ".json")
                with open(path, "w") as f:
                    json.dump({"storage": storage}, f)
                paths.append(path)
            return layouts.main(paths)

    def test_an_unchanged_layout_passes(self):
        self.assertEqual(self.check(BASE), 0)

    def test_ast_ids_do_not_count_as_a_change(self):
        moved = BASE[:2] + [entry("_ops", 2, "t_mapping(t_uint256,t_struct(Operation)9999_storage)"), gap(3, 40)]
        self.assertEqual(self.check(moved), 0)

    def test_variables_carved_from_the_gap_pass(self):
        self.assertEqual(self.check(BASE[:3] + [entry("_d", 3, "t_address"), gap(4, 39)]), 0)
        self.assertEqual(self.check(BASE[:3] + [entry("_d", 3, "t_address"), entry("_e", 4, "t_uint256"), gap(5, 38)]), 0)

    def test_every_other_change_fails(self):
        retyped = [dict(BASE[0], type="t_uint64")] + BASE[1:]
        cases = {
            "the gap shrank with nothing added": BASE[:3] + [gap(3, 39)],
            "a variable added without shrinking the gap": BASE[:3] + [entry("_d", 3, "t_address"), gap(4, 40)],
            "a variable appended after the gap": BASE + [entry("_d", 43, "t_address")],
            "a variable inserted in the middle": BASE[:1] + [entry("_d", 1, "t_address")]
            + [dict(e, slot=str(int(e["slot"]) + 1)) for e in BASE[1:3]] + [gap(4, 39)],
            "a variable retyped": retyped,
            "two variables reordered": [BASE[0], BASE[2], BASE[1], BASE[3]],
            "a variable removed": BASE[:2] + [gap(2, 41)],
            "the gap removed": BASE[:3] + [entry("_d", 3, "t_address")],
            "the gap grown": BASE[:3] + [gap(3, 41)],
        }
        for name, current in cases.items():
            with self.subTest(name):
                self.assertEqual(self.check(current), 1, name)

    def test_a_baseline_without_a_gap_allows_no_change(self):
        no_gap = BASE[:3]
        self.assertEqual(self.check(no_gap, baseline=no_gap), 0)
        self.assertEqual(self.check(no_gap + [entry("_d", 3, "t_address")], baseline=no_gap), 1)

    def test_an_empty_layout_is_an_error_not_a_match(self):
        self.assertEqual(self.check([], baseline=[]), 1)


if __name__ == "__main__":
    unittest.main()
