// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";

import {OracleRounds} from "../src/oracle/OracleRounds.sol";
import {OracleStaking} from "../src/oracle/OracleStaking.sol";
import {Roles} from "../src/shared/access/Roles.sol";
import {MockERC20} from "../test/utils/MockERC20.sol";

/// @notice Local-only oracle deployment for development and the end-to-end test.
/// @dev Never run this against a public network: it grants every role to one key.
contract DeployOracleLocal is Script {
    uint256 internal constant MIN_STAKE = 10_000e18;
    uint256 internal constant STAKE_FLOOR = 5_000e18;
    uint256 internal constant UNBONDING = 7 days;
    uint256 internal constant MAX_SLASH_BPS = 1_000;
    uint256 internal constant MAX_NODES = 31;

    uint256 internal constant ROUND_DURATION = 300;
    uint256 internal constant QUORUM_BPS = 6_667;
    uint256 internal constant MIN_QUORUM_NODES = 3;

    function run() external returns (address stakingProxy, address roundsProxy, address token) {
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);

        vm.startBroadcast(deployerKey);

        MockERC20 stakeToken = new MockERC20("Aegis Stake", "aSTK", 18);
        stakeToken.mint(deployer, 10_000_000e18);

        stakingProxy = address(
            new ERC1967Proxy(
                address(new OracleStaking()),
                abi.encodeCall(
                    OracleStaking.initialize,
                    (
                        deployer,
                        address(stakeToken),
                        MIN_STAKE,
                        STAKE_FLOOR,
                        UNBONDING,
                        MAX_SLASH_BPS,
                        MAX_NODES
                    )
                )
            )
        );

        roundsProxy = address(
            new ERC1967Proxy(
                address(new OracleRounds()),
                abi.encodeCall(
                    OracleRounds.initialize,
                    (deployer, stakingProxy, ROUND_DURATION, QUORUM_BPS, MIN_QUORUM_NODES)
                )
            )
        );

        OracleStaking(stakingProxy).grantRole(Roles.ORACLE_MANAGER_ROLE, deployer);
        OracleStaking(stakingProxy).grantRole(Roles.SLASHER_ROLE, deployer);
        OracleRounds(roundsProxy).grantRole(Roles.ORACLE_MANAGER_ROLE, deployer);
        OracleRounds(roundsProxy).grantRole(Roles.PAUSER_ROLE, deployer);

        OracleRounds(roundsProxy).registerFeed(keccak256("ETH/USD"), "ETH/USD", 18);

        vm.stopBroadcast();

        token = address(stakeToken);
        _writeArtifact(stakingProxy, roundsProxy, token);

        console2.log("stake token:    ", token);
        console2.log("OracleStaking:  ", stakingProxy);
        console2.log("OracleRounds:   ", roundsProxy);
    }

    function _writeArtifact(
        address staking,
        address rounds,
        address token
    ) internal {
        string memory key = "oracle";
        vm.serializeAddress(key, "oracleStaking", staking);
        vm.serializeAddress(key, "oracleRounds", rounds);
        vm.serializeAddress(key, "stakeToken", token);
        vm.serializeUint(key, "chainId", block.chainid);
        string memory out = vm.serializeUint(key, "deployedAtBlock", block.number);

        vm.writeJson(out, string.concat("./deployments/oracle-", vm.toString(block.chainid), ".json"));
    }
}
