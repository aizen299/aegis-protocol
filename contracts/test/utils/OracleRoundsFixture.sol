// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {OracleRounds} from "../../src/oracle/OracleRounds.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {OracleFixture} from "./OracleFixture.sol";

abstract contract OracleRoundsFixture is OracleFixture {
    uint256 internal constant ROUND_DURATION = 300;
    uint256 internal constant QUORUM_BPS = 6_667;
    uint256 internal constant MIN_QUORUM_NODES = 3;

    bytes32 internal constant FEED = keccak256("ETH/USD");

    OracleRounds internal rounds;

    struct Node {
        address addr;
        uint256 key;
    }

    Node[] internal keyed;

    function setUp() public virtual override {
        super.setUp();

        OracleRounds implementation = new OracleRounds();
        bytes memory initData = abi.encodeCall(
            OracleRounds.initialize, (admin, address(staking), ROUND_DURATION, QUORUM_BPS, MIN_QUORUM_NODES)
        );
        rounds = OracleRounds(address(new ERC1967Proxy(address(implementation), initData)));

        vm.startPrank(admin);
        rounds.grantRole(Roles.ORACLE_MANAGER_ROLE, oracleManager);
        rounds.grantRole(Roles.PAUSER_ROLE, admin);
        rounds.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();

        vm.prank(oracleManager);
        rounds.registerFeed(FEED, "ETH/USD");
    }

    /// @dev Registers `count` keyed nodes so their submissions can actually be signed.
    function _spawnNodes(
        uint256 count
    ) internal {
        for (uint256 i = 0; i < count; i++) {
            (address addr, uint256 key) = makeAddrAndKey(string.concat("keyed-node-", vm.toString(i)));
            keyed.push(Node({addr: addr, key: key}));
            _registerNode(addr, MIN_STAKE);
        }
    }

    function _sign(
        Node memory node,
        uint256 roundId,
        uint256 value
    ) internal view returns (bytes memory) {
        uint256 nonce = rounds.nonceOf(node.addr);
        bytes32 digest = rounds.submissionDigest(roundId, FEED, value, node.addr, nonce);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(node.key, digest);
        return abi.encodePacked(r, s, v);
    }

    function _submit(
        Node memory node,
        uint256 roundId,
        uint256 value
    ) internal {
        bytes memory signature = _sign(node, roundId, value);
        // Read the nonce before the prank: `nonceOf` is an external call, and a call in argument
        // position consumes the prank, so `submit` would run as the test contract.
        uint256 nonce = rounds.nonceOf(node.addr);
        vm.prank(node.addr);
        rounds.submit(roundId, value, nonce, signature);
    }
}
